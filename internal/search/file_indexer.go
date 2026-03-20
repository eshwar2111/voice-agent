package search

import (
	"log"
	"os"
	"path/filepath"
	"strings"
)

type FileRecord struct {
	Path string
	Name string
}

var index map[string][]FileRecord
var IsReady bool

func InitIndexer(rootDir string) {
	index = make(map[string][]FileRecord)
	IsReady = false

	go func() {
		log.Printf("Starting file indexer on %s...", rootDir)
		_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			// Skip hidden directories and large system folders to speed up MVP indexing
			if info.IsDir() && (strings.HasPrefix(info.Name(), ".") || info.Name() == "AppData" || info.Name() == "Windows") {
				return filepath.SkipDir
			}

			name := strings.ToLower(info.Name())
			index[name] = append(index[name], FileRecord{
				Path: path,
				Name: info.Name(),
			})
			return nil
		})
		log.Printf("File indexer finished. Indexed items in %s.", rootDir)
		IsReady = true
	}()
}
