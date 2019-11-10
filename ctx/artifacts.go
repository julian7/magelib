package ctx

import "fmt"

const (
	_ = iota
	// FormatRaw represents an artifact in its pristine format (eg. binary or ar archive)
	FormatRaw
	// FormatGZip represents an artifact in compressed format (eg. the raw format compressed with gzip)
	FormatGZip
	// FormatUPX represents an artifact compressed by UPX (self-uncompressing executable)
	FormatUPX
	// FormatTar represents an artifact put together into a TAR archive. It can be further compressed.
	FormatTar
	// FormatZip represents an artifact put together into a ZIP archive.
	FormatZip
)

type (
	// Artifacts is a slice of Artifact
	Artifacts []*Artifact

	// Artifact is a file generated by the build pipeline, which can
	// be further processed by later steps (eg. a build result put into
	// an archive)
	Artifact struct {
		Arch     string
		Filename string
		Format   int
		Location string
		Name     string
		OS       string
	}
)

// Add registers a new artifact in Artifacts
func (arts *Artifacts) Add(artifact *Artifact) {
	*arts = append(*arts, artifact)
}

// ByName searches artifacts by their build names
func (arts *Artifacts) ByName(name string) *Artifacts {
	results := &Artifacts{}
	for i := range *arts {
		if (*arts)[i].Name == name {
			*results = append(*results, (*arts)[i])
		}
	}
	return results
}

// OsArch returns artifact's os-arch string
func (art *Artifact) OsArch() string {
	return fmt.Sprintf("%s-%s", art.OS, art.Arch)
}
