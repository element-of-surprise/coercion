// Reporter pvovides a binary file that will read the reports from a sub-directory
// called "report" and serve them on a web server. This defaults to port 8080.
package main

import (
	"flag"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"

	"github.com/spf13/afero"
)

var port = flag.String("port", ":8080", "port to listen on")
var dir = flag.String("dir", "./html", "directory to serve")

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

func main() {
	flag.Parse()

	osfs := afero.NewOsFs()
	base := afero.NewBasePathFs(osfs, *dir)

	cacheFS := afero.NewCacheOnReadFs(base, afero.NewMemMapFs(), 30*time.Second)

	rfs := FS{cacheFS}

	fs.WalkDir(rfs, ".", func(path string, d fs.DirEntry, err error) error {
		log.Println(path)
		return nil
	})

	app := fiber.New()
	app.Use(
		"/",
		filesystem.New(
			filesystem.Config{
				Root:  http.FS(rfs),
				Index: "plan.html",
			},
		),
	)
	app.Listen(*port)
}
