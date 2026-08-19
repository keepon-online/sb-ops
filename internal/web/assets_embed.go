package web

import (
	"embed"
	"io/fs"
	"log"
)

//go:embed embedded/*
var embeddedFrontend embed.FS

func embeddedFrontendAssets() fs.FS {
	sub, err := fs.Sub(embeddedFrontend, "embedded")
	if err != nil {
		log.Printf("embedded frontend fs unavailable: %v", err)
		return emptyFS{}
	}
	return sub
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}
