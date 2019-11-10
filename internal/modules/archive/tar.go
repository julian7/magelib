package archive

import (
	"archive/tar"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/julian7/magelib/ctx"
	"github.com/julian7/magelib/modules"
)

type (
	// Tar is a module for building an archive from prior builds
	Tar struct {
		// Builds specifies which build names should be added to the archive.
		Builds []string
		// CommonDir contains a common directory name for all files inside
		// the tar archive. An empty CommonDir skips creating subdirectories.
		// Default: `{{.ProjectName}}-{{.Version}}-{{.OS}}-{{.Arch}}`.
		CommonDir string
		// Compression specifies which compression should be applied to the
		// archive.
		Compression Compression
		// Files contains a list of static files should be added to the
		// archive file. They are interpretered as glob.
		Files []string
		// Name contains the artifact's name used by later stages of
		// the build pipeline. Archives, ReleaseNotes, and Publishes
		// may refer to this name for referencing build results.
		// Default: "archive".
		Name string
		// Output is where the build writes its output. Default:
		// `{{.ProjectName}}-{{.Version}}-{{.OS}}-{{.Arch}}.tar{{.Ext}}`
		// where `{{.Ext}}` contains the compression's default extension
		Output string
		// Skip specifies GOOS-GOArch combinations to be skipped.
		// They are in `{{.Os}}-{{.Arch}}` format.
		// It filters builds to be included.
		Skip []string
	}
)

func init() {
	modules.RegisterModule(&modules.PluggableModule{
		Kind:    "archive:tar",
		Factory: NewTar,
		Deps:    []string{"setup:git_tag"},
	})
}

func NewTar() modules.Pluggable {
	return &Tar{
		Builds:      []string{"default"},
		CommonDir:   "{{.ProjectName}}-{{.Version}}-{{.OS}}-{{.Arch}}",
		Compression: Compression{&CompressNONE{}},
		Files:       []string{"README*"},
		Name:        "archive",
		Output:      "{{.ProjectName}}-{{.Version}}-{{.OS}}-{{.Arch}}.tar{{.Ext}}",
		Skip:        []string{},
	}
}

type tarRuntime struct {
	*Tar
	*ctx.Context
	targets map[string]*ctx.Artifacts
}

func (archive *Tar) Run(context *ctx.Context) error {
	runtime, err := archive.newRuntime(context)

	if err != nil {
		return err
	}

	return runtime.run()
}

func (archive *Tar) newRuntime(context *ctx.Context) (*tarRuntime, error) {
	skipIndex := make(map[string]bool, len(archive.Skip))

	for _, skip := range archive.Skip {
		skipIndex[skip] = true
	}

	builds := map[string]*ctx.Artifacts{}

	for _, build := range archive.Builds {
		for _, art := range *context.Artifacts.ByName(build) {
			osarch := fmt.Sprintf("%s-%s", art.OS, art.Arch)
			if _, ok := skipIndex[osarch]; ok {
				continue
			}

			if _, ok := builds[osarch]; !ok {
				builds[osarch] = &ctx.Artifacts{}
			}
			*builds[osarch] = append(*builds[osarch], art)
		}
	}

	numTargets := 0
	lastosarch := ""

	for osarch := range builds {
		targets := len(*builds[osarch])
		if numTargets == 0 {
			numTargets = targets
			lastosarch = osarch
			continue
		}
		if numTargets < targets {
			return nil, errNumTargets(lastosarch, osarch, builds)
		}
		if numTargets > targets {
			return nil, errNumTargets(osarch, lastosarch, builds)
		}
	}

	return &tarRuntime{
		Tar:     archive,
		Context: context,
		targets: builds,
	}, nil
}

func (rt *tarRuntime) run() error {
	for osarch := range rt.targets {
		target, err := rt.singleTarget(osarch)
		if err != nil {
			return err
		}

		if err := target.run(); err != nil {
			return err
		}
	}

	return nil
}

type singleTarget struct {
	*ctx.Context
	Arch        string
	CommonDir   string
	Compression Compression
	DirsWritten map[string]bool
	Files       []string
	Name        string
	OS          string
	Output      string
	Targets     *ctx.Artifacts
}

func (rt *tarRuntime) singleTarget(osarch string) (*singleTarget, error) {
	artifacts, ok := rt.targets[osarch]
	if !ok || len(*artifacts) == 0 {
		return nil, fmt.Errorf("empty target for os-arch %s", osarch)
	}

	ret := &singleTarget{
		Context:     rt.Context,
		Arch:        (*artifacts)[0].Arch,
		Compression: rt.Tar.Compression,
		DirsWritten: map[string]bool{},
		Files:       make([]string, len(rt.Files)),
		Name:        rt.Tar.Name,
		OS:          (*artifacts)[0].OS,
		Targets:     artifacts,
	}
	for i := range rt.Files {
		ret.Files[i] = rt.Files[i]
	}

	td := &modules.TemplateData{
		Arch:        ret.Arch,
		ProjectName: rt.Context.ProjectName,
		OS:          ret.OS,
		Version:     rt.Context.Version,
		Ext:         rt.Compression.Extension(),
	}

	for _, task := range []struct {
		name   string
		source string
		target *string
	}{
		{"commondir", rt.Tar.CommonDir, &ret.CommonDir},
		{"output", path.Join(rt.Context.TargetDir, rt.Tar.Output), &ret.Output},
	} {
		var err error

		*task.target, err = td.Parse(
			fmt.Sprintf("archivetar-%s-%s-%s-%s", rt.Tar.Name, ret.OS, ret.Arch, task.name),
			task.source,
		)
		if err != nil {
			return nil, fmt.Errorf("rendering %q: %w", task.source, err)
		}
	}
	ret.Output = path.Clean(ret.Output)

	return ret, nil
}

func (target *singleTarget) run() error {
	archive, err := os.Create(target.Output)
	if err != nil {
		return fmt.Errorf("cannot create archive file %s: %w", target.Output, err)
	}

	defer archive.Close()

	compressedArchive := target.Compression.Writer(archive)
	defer compressedArchive.Close()

	tw := tar.NewWriter(compressedArchive)
	defer tw.Close()

	for _, artifact := range *target.Targets {
		if err := target.writeArtifact(tw, artifact); err != nil {
			return fmt.Errorf("writing %s: %w", target.Output, err)
		}
	}

	for _, file := range target.Files {
		if err := target.writeFileGlob(tw, file); err != nil {
			return fmt.Errorf("writing %s: %w", target.Output, err)
		}
	}

	context.Artifacts.Add(&ctx.Artifact{
		Arch:     target.Arch,
		Filename: target.Output,
		Format:   ctx.FormatTar,
		Location: target.Output,
		Name:     target.Name,
		OS:       target.OS,
	})

	return nil
}

func (target *singleTarget) writeArtifact(tw *tar.Writer, artifact *ctx.Artifact) error {
	filename := path.Join(
		target.CommonDir,
		strings.TrimPrefix(artifact.Filename, artifact.Location+"/"),
	)
	if err := target.writeDirs(tw, path.Dir(filename)); err != nil {
		return err
	}

	return target.writeFile(tw, filename, artifact.Filename)
}

func (target *singleTarget) writeFileGlob(tw *tar.Writer, source string) error {
	matches, err := filepath.Glob(source)
	if err != nil {
		return err
	}

	for _, filename := range matches {
		fullfn := path.Join(target.CommonDir, filename)
		if err := target.writeDirs(tw, path.Dir(fullfn)); err != nil {
			return err
		}

		if err := target.writeFile(tw, fullfn, filename); err != nil {
			return err
		}
	}

	return nil
}

func (target *singleTarget) writeFile(tw *tar.Writer, destpath, source string) error {
	fi, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("can't stat file %s: %w", source, err)
	}

	hdr, err := tar.FileInfoHeader(fi, "")
	if err != nil {
		return err
	}

	hdr.Name = destpath

	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	sourceReader, err := os.Open(source)
	if err != nil {
		return err
	}

	defer sourceReader.Close()

	buf := make([]byte, 4096)

	if n, err := io.CopyBuffer(tw, sourceReader, buf); err != nil {
		return fmt.Errorf(
			"copying %s to archive %s (%d bytes written, %d bytes reported): %w",
			source,
			destpath,
			n,
			fi.Size(),
			err,
		)
	}

	return nil
}

func (target *singleTarget) writeDirs(tw *tar.Writer, fullpath string) error {
	if fullpath == "." {
		return nil
	}
	dirs := []string{fullpath}
	for {
		fullpath = path.Dir(fullpath)
		if fullpath == "." {
			break
		}
		dirs = append(dirs, fullpath)
	}

	for i := range dirs {
		dirname := dirs[len(dirs)-i-1]

		if err := target.writeDir(tw, dirname, 0o755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", dirname, err)
		}
	}

	return nil
}

func (target *singleTarget) writeDir(tw *tar.Writer, dirname string, mode int64) error {
	if _, ok := target.DirsWritten[dirname]; ok {
		return nil
	}

	st, err := os.Stat(target.TargetDir)
	if err != nil {
		return err
	}

	hdr, err := tar.FileInfoHeader(st, "")
	if err != nil {
		return err
	}

	hdr.Name = dirname + "/"

	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}

	target.DirsWritten[dirname] = true

	return nil
}

func errNumTargets(bad, good string, builds map[string]*ctx.Artifacts) error {
	targets := map[string]bool{}
	if len(builds) == 0 {
		return fmt.Errorf("no builds found")
	}

	_, ok := builds[good]
	if !ok || len(*builds[good]) == 0 {
		return fmt.Errorf("invalid reference to good artifacts: %s", good)
	}

	for _, art := range *builds[good] {
		targets[art.OsArch()] = true
	}

	_, ok = builds[bad]
	if ok && len(*builds[bad]) > 0 {
		for _, art := range *builds[bad] {
			targets[art.OsArch()] = false
		}
	}

	var goodTargets, badTargets []string
	for name := range targets {
		if targets[name] {
			goodTargets = append(goodTargets, name)
		} else {
			badTargets = append(badTargets, name)
		}
	}
	sort.Strings(goodTargets)
	sort.Strings(badTargets)

	if len(badTargets) == 0 {
		return fmt.Errorf(
			"no targets found for build%s %s",
			map[bool]string{true: "", false: "s"}[len(goodTargets) == 1],
			strings.Join(goodTargets, ", "),
		)
	}

	return fmt.Errorf(
		"build %s is missing os-arch target%s %s",
		bad,
		map[bool]string{true: "", false: "s"}[len(goodTargets) == 1],
		strings.Join(goodTargets, ", "),
	)
}
