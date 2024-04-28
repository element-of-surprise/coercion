package html

import (
	"bytes"
	"html/template"
	"log"
	"reflect"
	"sync"
	"time"

	"github.com/element-of-surprise/workstream/workflow"

	_ "embed"
)

//go:embed embed/tmpl/plan.tmpl
var tmplText string

//go:embed embed/imgs/banner_img.svg
var bannerSVG string

var tmpl *template.Template

func init() {
	var err error
	tmpl, err = template.New("").Funcs(
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
		},
	).Parse(tmplText)
	if err != nil {
		panic(err)
	}
}

// BufPool is a pool of *bytes.Buffer objects. You may use this to loweer
// the number of allocations when rendering Plans
type BufPool struct {
	pool sync.Pool
}

// Get returns a *bytes.Buffer from the pool. The buffer will be reset.
func (bp *BufPool) Get() *bytes.Buffer {
	return bp.pool.Get().(*bytes.Buffer)
}

// Put returns a *bytes.Buffer to the pool. The buffer is reset.
func (bp *BufPool) Put(b *bytes.Buffer) {
	b.Reset()

	bp.pool.Put(b)
}

type renderOptions struct {
	// bufPool is the buffer pool to use for rendering.
	bufPool *BufPool
}

// Option is an optional argument for Render.
type Option func(renderOptions) (renderOptions, error)

// WithBufPool sets a buffer pool to use for rendering.
func WithBufPool(bp *BufPool) Option {
	return func(o renderOptions) (renderOptions, error) {
		o.bufPool = bp
		return o, nil
	}
}

// Render renders a workflow.Plan to an HTML document. This may alter the Plan object to
// eliminate Request and Response fields that have fields marked with the `coerce:"secure"` tag.
func Render(plan *workflow.Plan, options ...Option) (*bytes.Buffer, error) {
	opts := renderOptions{}
	for _, opt := range options {
		var err error
		opts, err = opt(opts)
		if err != nil {
			return nil, err
		}
	}

	var b *bytes.Buffer
	if opts.bufPool == nil {
		b = &bytes.Buffer{}
	} else {
		b = opts.bufPool.Get()
	}

	coerceSecure(plan)

	if err := tmpl.Execute(b, plan); err != nil {
		panic(err)
	}
	return b, nil
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
	return template.HTML(bannerSVG)
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

// coerceSecure sets all fields in a struct that are tagged with `coerce:"secure"` to their zero value.
func coerceSecure(v interface{}) {
	val := reflect.ValueOf(v)
	if val.Kind() == reflect.Ptr {
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return
	}

	typ := val.Type()
	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if field.CanSet() {
			tag := typ.Field(i).Tag.Get("coerce")
			if tag == "secure" {
				field.Set(reflect.Zero(field.Type()))
				continue
			}

			// Recursively coerce nested structs
			if field.Kind() == reflect.Struct || (field.Kind() == reflect.Ptr && field.Elem().Kind() == reflect.Struct) {
				coerceSecure(field.Addr().Interface())
			}
		}
	}
}
