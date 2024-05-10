package embeded

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/element-of-surprise/coercion/workflow"
)

// Tmpls is a collection of templates that are embedded in the binary.
var Tmpls = template.New("")

func init() {
	walkErr := fs.WalkDir(
		FS,
		"tmpl",
		func(path string, d fs.DirEntry, err error) error {
			if d.IsDir() {
				return nil
			}

			tmplText, err := fs.ReadFile(FS, path)
			if err != nil {
				return fmt.Errorf("reading embedded template failed(%s): %w", path, err)
			}

			log.Println(filepath.Base(path))
			Tmpls, err = Tmpls.New(filepath.Base(path)).Funcs(
				template.FuncMap{
					"completedPlan":      calcCompleted[*workflow.Plan],
					"completedBlock":     calcCompleted[*workflow.Block],
					"completedChecks":    calcCompleted[*workflow.Checks],
					"completedSequences": calcCompleted[[]*workflow.Sequence],
					"completedSequence":  calcCompleted[*workflow.Sequence],
					"attemptStatus":      attemptStatus,
					"banner":             banner,
					"time":               timeOutput,
					"statusColor":        statusColor,
					"mod":                mod,
					"isZeroTime":         isZeroTime,
					"jsonMarshal":        jsonMarshal,
				},
			).Parse(string(tmplText))
			if err != nil {
				return fmt.Errorf("parsing template failed(%s): %w", path, err)
			}
			return nil
		},
	)
	if walkErr != nil {
		panic(walkErr)
	}
}

type supportedCalcs interface {
	*workflow.Plan | *workflow.Block | *workflow.Checks | *workflow.Sequence | []*workflow.Sequence
}

type completed struct {
	// Total is the total number of items.
	Total int
	// Completed is the number of items that are in a completed state.
	Completed int
	// Failed is the number of items that are in a failed state.
	Failed int
	// Running is the number of items that are in a running state.
	Running int
	// Percent is the percentage of items that are in a completed state.
	Percent int
}

func (c completed) Done() int {
	return c.Completed + c.Failed
}

func (c completed) Color() template.HTMLAttr {
	if c.Failed > 0 {
		return template.HTMLAttr("red")
	}
	if c.Running > 0 {
		return template.HTMLAttr("yellow")
	}
	return template.HTMLAttr("green")
}

// calcCompleted calculates the number of completed items in a workflow object,
// and returns the number of completed items and the total number of items.
func calcCompleted[T supportedCalcs](v T) completed {
	var a any = v

	c := completed{}
	switch x := a.(type) {
	case *workflow.Plan:
		for _, b := range x.Blocks {
			switch b.State.Status {
			case workflow.Completed:
				c.Completed++
			case workflow.Running:
				c.Running++
			case workflow.Failed:
				c.Failed++
			}
		}
		c.Total = len(x.Blocks)
	case *workflow.Block:
		for _, seq := range x.Sequences {
			switch seq.State.Status {
			case workflow.Running:
				c.Running++
			case workflow.Failed:
				c.Failed++
			case workflow.Completed:
				c.Completed++
			}
		}
		c.Total = len(x.Sequences)
	case *workflow.Checks:
		for _, act := range x.Actions {
			switch act.State.Status {
			case workflow.Running:
				c.Running++
			case workflow.Failed:
				c.Failed++
			case workflow.Completed:
				c.Completed++
			}
		}
		c.Total = len(x.Actions)
	case []*workflow.Sequence:
		for _, seq := range x {
			switch seq.State.Status {
			case workflow.Running:
				c.Running++
			case workflow.Failed:
				c.Failed++
			case workflow.Completed:
				c.Completed++
			}
		}
		log.Printf("%+v", c)
		c.Total = len(x)
		log.Printf("%v/%v", c.Done(), c.Total)
	case *workflow.Sequence:
		for _, act := range x.Actions {
			switch act.State.Status {
			case workflow.Running:
				c.Running++
			case workflow.Failed:
				c.Failed++
			case workflow.Completed:
				c.Completed++
			}
		}
		c.Total = len(x.Actions)
	default:
		panic("unsupported type")
	}
	c.Percent = (c.Completed * 100) / c.Total
	return c
}

func attemptStatus(a *workflow.Attempt) string {
	if a.Err != nil {
		return "Failed"
	}
	return "Success"
}

func banner() template.HTML {
	b, err := FS.ReadFile("imgs/banner_img.svg")
	if err != nil {
		panic(err)
	}
	return template.HTML(b)
}

func timeOutput(t time.Time) string {
	t = t.UTC()
	return t.Format("2006-01-02 15:04:05 UTC")
}

func statusColor(s workflow.Status) template.HTMLAttr {
	switch s {
	case workflow.NotStarted:
		return template.HTMLAttr("gray")
	case workflow.Failed:
		return template.HTMLAttr("red")
	case workflow.Completed:
		return template.HTMLAttr("green")
	default:
		return template.HTMLAttr("blue")
	}
}

func mod(a int) int {
	return a % 2
}

func isZeroTime(t time.Time) bool {
	return t.IsZero()
}

func jsonMarshal(v any) string {
	if v == nil {
		return ""
	}

	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("error marshalling JSON: %v", err)
	}

	return bytesToStr(b)
}

func bytesToStr(b []byte) string {
	return unsafe.String(unsafe.SliceData(b), len(b))
}
