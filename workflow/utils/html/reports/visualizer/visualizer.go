package main

import (
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/element-of-surprise/coercion/workflow"
	"github.com/element-of-surprise/coercion/workflow/utils/html/reports"
	"github.com/google/uuid"

	"github.com/go-json-experiment/json"
	"github.com/gofiber/fiber/v2"
	"github.com/spf13/afero"

	_ "embed"
)

var (
	addr    = flag.String("addr", ":3000", "the host:port to listen on")
	uploads = flag.String("uploads", "", "custom directory to store uploaded files, by default this is [execdir]/uploads")
)

//go:embed html/index/index.tmpl
var indexTmpl string

//go:embed html/index/index.css
var indexCSS []byte

//go:embed html/index/uploader.hs
var uploaderHS []byte

var uploadsPath string

type indexArgs struct {
	Plans []string
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	uploadsPath = *uploads
	if uploadsPath == "" {
		binPath, err := os.Executable()
		if err != nil {
			log.Fatalf("failed to get executable path: %v", err)
		}
		binPath = filepath.Dir(binPath)
		uploadsPath = filepath.Join(binPath, "uploads")
	}

	if err := os.MkdirAll(uploadsPath, 0770); err != nil {
		log.Fatalf("failed to create uploads directory: %v", err)
	}

	app := newApp()

	log.Fatal(app.Listen(*addr))
}

type app struct {
	app *fiber.App

	osfs afero.Fs
}

func newApp() app {
	tmpls := template.Must(template.New("index.tmpl").Parse(indexTmpl))

	app := app{
		app: fiber.New(
			fiber.Config{
				Prefork: true,
				ErrorHandler: func(c *fiber.Ctx, err error) error {
					fiber.DefaultErrorHandler(c, err)
					log.Println(err)
					return nil
				},
			},
		),
		osfs: afero.NewBasePathFs(afero.NewOsFs(), uploadsPath),
	}
	app.app.Get("/", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/html")

		files, err := afero.ReadDir(app.osfs, ".")
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Error reading directory: %v", err))
		}

		args := indexArgs{}
		for _, file := range files {
			if file.IsDir() {
				args.Plans = append(args.Plans, file.Name())
			}
		}

		err = tmpls.ExecuteTemplate(c, "index.tmpl", args)
		if err != nil {
			return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("Error rendering template: %v", err))
		}

		return c.SendStatus(fiber.StatusOK)
	})

	app.app.Get("/uploader.css", func(c *fiber.Ctx) error {
		return c.Send(indexCSS)
	})

	app.app.Get("/uploader.hs", func(c *fiber.Ctx) error {
		return c.Send(uploaderHS)
	})

	app.app.Post("/upload", app.handleUpload)

	app.app.Use(
		"/view/",
		app.view,
	)

	return app
}

func (a app) Listen(port string) error {
	return a.app.Listen(port)
}

// handleUpload handles the upload of a plan object in JSON form and converts it to html pages
// for visualization.
func (a app) handleUpload(c *fiber.Ctx) error {
	plan := &workflow.Plan{}

	body, err := fileContent(c)
	if err != nil {
		return err
	}

	err = json.Unmarshal(body, plan)
	if err != nil {
		log.Println("failed to unmarshal plan: ", err)
		return err
	}

	if plan.ID == uuid.Nil {
		return fiber.NewError(fiber.StatusBadRequest, "plan ID is required")
	}
	dirPath := filepath.Join(uploadsPath, plan.ID.String())
	if err := os.MkdirAll(dirPath, 0770); err != nil {
		return fmt.Errorf("cannot create directory(%s): %v", dirPath, err)
	}

	f, err := reports.Render(c.Context(), plan)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, fmt.Sprintf("failed to render plan: %v", err))
	}

	walkErr := fs.WalkDir(
		f,
		".",
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				if path == "." {
					return nil
				}
				p := filepath.Clean(filepath.Join(dirPath, path))
				err := os.Mkdir(p, 0770)
				if err != nil && !os.IsExist(err) {
					return fmt.Errorf("problem creating directory(%s) : %w", p, err)
				}
				return nil
			}

			data, err := afero.ReadFile(f.(reports.FS).FS(), path)
			if err != nil {
				return fmt.Errorf("problem reading file(%s) for tarball: %w", path, err)
			}

			err = os.WriteFile(filepath.Join(uploadsPath, plan.ID.String(), path), data, 0660)
			if err != nil {
				return fmt.Errorf("problem writing file(%s) to tarball: %w", path, err)
			}
			return nil
		},
	)
	if walkErr != nil {
		return fiber.NewError(fiber.StatusInternalServerError, walkErr.Error())
	}

	return nil
}

// view provides viewing of the uploaded files.
func (a app) view(c *fiber.Ctx) error {
	s := strings.Split(c.Path(), "view/")
	if len(s) != 2 {
		return fiber.NewError(fiber.StatusBadRequest, "invalid path")
	}
	s2 := strings.Split(s[1], "/")
	ustr := strings.TrimSuffix(s2[0], "/")

	id, err := uuid.Parse(ustr)
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("invalid id: %v", err))
	}

	if fi, err := a.osfs.Stat(id.String()); err != nil || !fi.IsDir() {
		return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("invalid upload path: %v", err))
	}

	ref := afero.NewBasePathFs(a.osfs, id.String())
	b, err := afero.ReadFile(ref, filepath.Join(s2[1:]...))
	if err != nil {
		return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("invalid file path: %v", err))
	}

	c.Set("Content-Type", "text/html")
	return c.Send(b)
}

// fileContent reads the content of a file from a multipart form request.
func fileContent(c *fiber.Ctx) ([]byte, error) {
	// Parse the multipart form data
	form, err := c.MultipartForm()
	if err != nil {
		return nil, c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}

	// Extract the file from the form
	files := form.File["file"]
	if len(files) == 0 {
		return nil, c.Status(fiber.StatusBadRequest).SendString("No file uploaded")
	}

	// Read the content of the file
	file, err := files[0].Open()
	if err != nil {
		return nil, c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}
	defer file.Close()

	// Read the file content
	fileContent, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, c.Status(fiber.StatusInternalServerError).SendString(err.Error())
	}
	return fileContent, nil
}

// FS is a wrapper around afero.Fs to implement fs.FS interface.
type FS struct {
	fs afero.Fs
}

func (f FS) Open(name string) (fs.File, error) {
	return f.fs.Open(name)
}

func (f FS) ReadFile(name string) ([]byte, error) {
	return afero.ReadFile(f.fs, name)
}
