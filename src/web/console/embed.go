package console

import (
	"embed"
	"io/fs"
)

//go:embed assets/*
var embedded embed.FS

func Assets() (fs.FS, error) {
	return fs.Sub(embedded, "assets")
}
