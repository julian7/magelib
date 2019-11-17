// modules provides an extendable interface for executable components in the
// build pipeline.
package modules

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/julian7/goshipdone/ctx"
	"gopkg.in/yaml.v3"
)

type (
	// Pluggable is a module, which can be pluggable into a pipeline
	Pluggable interface {
		Run(*ctx.Context) error
	}

	// Modules is a list of Module-s of a single stage
	Modules struct {
		Stage   string `yaml:"-"`
		SkipFn  func(*ctx.Context) bool
		Modules []Module `yaml:"-"`
		loaded  map[string]bool
	}

	// Module is a single module, specifying its type and its Pluggable
	Module struct {
		Type string
		Pluggable
	}
)

// NewModules is generating a new, empty Modules of a certain stage
func NewModules(stage string) *Modules {
	return &Modules{Stage: stage}
}

// UnmarshalYAML parses YAML node to load its modules
func (mod *Modules) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("definition of `%s` is not a sequence", mod.Stage)
	}

	for idx, child := range node.Content {
		if child.Kind != yaml.MappingNode {
			return fmt.Errorf("item #%d of `%s` definition is not a map", idx+1, mod.Stage)
		}

		itemType, err := getType(child)

		if err != nil {
			return fmt.Errorf(
				"definition %s, item #%d: %w",
				mod.Stage,
				idx+1,
				err,
			)
		}

		if err := mod.Add(itemType, child, false); err != nil {
			return err
		}
	}

	return nil
}

// Add adds a single module into Modules, decoding a YAML node if provided.
// It is also able to register a node only if not yet registered.
// By default, Modules allows registration of its own stage only, but
// modules registered for all stages are also accepted, if there is no
// specific module registration exists.
//
// Eg. if there are two different modules registered for "*:dump" and
// "build:dump", a reference to "dump" kind in archives will fire "*:dump"
// module, but a similar "dump" kind in builds will fire "build:dump".
func (mod *Modules) Add(itemType string, node *yaml.Node, once bool) error {
	var kind string

	var targetModFactory PluggableFactory

	var ok bool

	for _, stage := range []string{mod.Stage, "*"} {
		kind = fmt.Sprintf("%s:%s", stage, itemType)

		targetModFactory, ok = LookupModule(kind)
		if ok {
			break
		}
	}

	if !ok {
		return fmt.Errorf("unknown module %s:%s", mod.Stage, itemType)
	}

	if once && mod.isLoaded(kind) {
		return fmt.Errorf("module %s already loaded", kind)
	}

	targetMod := targetModFactory()

	if node != nil {
		if err := node.Decode(targetMod); err != nil {
			return fmt.Errorf("cannot decode module %s: %w", kind, err)
		}
	}

	mod.Modules = append(mod.Modules, Module{
		Type:      itemType,
		Pluggable: targetMod,
	})

	mod.flagLoaded(kind)

	return nil
}

func (mod *Modules) isLoaded(kind string) bool {
	if mod.loaded == nil {
		return false
	}

	_, loaded := mod.loaded[kind]

	return loaded
}

func (mod *Modules) flagLoaded(kind string) {
	if mod.loaded == nil {
		mod.loaded = map[string]bool{}
	}

	mod.loaded[kind] = true
}

func (mod *Modules) String() string {
	mods := make([]string, 0, len(mod.Modules))
	for _, module := range mod.Modules {
		mods = append(mods, fmt.Sprintf("%+v", module))
	}

	return fmt.Sprintf(
		"{%s [%s]}",
		mod.Stage,
		strings.Join(mods, " "),
	)
}

// Run goes through all internally loaded modules, and run them
// one by one.
func (mod *Modules) Run(context *ctx.Context) error {
	log.Printf("====> %s", strings.ToUpper(mod.Stage))

	startMod := time.Now()

	if mod.SkipFn != nil && mod.SkipFn(context) {
		log.Printf("SKIPPED")
	} else if err := mod.run(context); err != nil {
		return err
	}

	log.Printf("<==== %s done in %s", strings.ToUpper(mod.Stage), time.Since(startMod))

	return nil
}

func (mod *Modules) run(context *ctx.Context) error {
	for _, module := range mod.Modules {
		log.Printf("----> %s", module.Type)

		start := time.Now()

		if err := module.Pluggable.Run(context); err != nil {
			return fmt.Errorf("%s:%s: %w", mod.Stage, module.Type, err)
		}

		log.Printf("<---- %s done in %s", module.Type, time.Since(start))
	}

	return nil
}

func getType(node *yaml.Node) (string, error) {
	var itemType string

	for idx := 0; idx < len(node.Content); idx += 2 {
		key := node.Content[idx]
		val := node.Content[idx+1]

		if key.Value == "type" {
			itemType = val.Value
			return itemType, nil
		}
	}

	return "", errors.New("type not defined")
}
