package main

import (
	"os"
	"path/filepath"
	"strings"
)

// FileChunk is one scorable unit. SPIKE: chunks are whole files; the real
// design chunks at function/method level.
type FileChunk struct {
	Path    string
	Content string
	Tokens  int64 // filled in by count_tokens
	// Pass-2 results:
	Category   string
	Multiplier float64
	Reasoning  string
	Evidence   string
	Err        error
}

// codeExts is the allowlist of source extensions we score.
var codeExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".rb": true, ".rs": true, ".java": true, ".kt": true,
	".c": true, ".cc": true, ".cpp": true, ".h": true, ".hpp": true,
	".cs": true, ".php": true, ".swift": true, ".scala": true,
	".sh": true, ".sql": true, ".vue": true, ".svelte": true, ".lua": true,
}

// skipDirs are never descended into.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true,
	"build": true, "out": true, ".next": true, "target": true,
	"__pycache__": true, ".venv": true, "venv": true, ".idea": true,
	".vscode": true, "coverage": true, ".turbo": true,
}

const maxFileBytes = 60 * 1024 // skip very large / generated files

// ingest walks root and returns all scorable source files (the caller caps).
func ingest(root string) ([]*FileChunk, error) {
	var chunks []*FileChunk
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !codeExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() == 0 || info.Size() > maxFileBytes {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// Skip obviously-minified/generated single-line blobs.
		if looksMinified(b) {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		chunks = append(chunks, &FileChunk{Path: filepath.ToSlash(rel), Content: string(b)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return chunks, nil
}

// looksMinified flags files whose average line is very long (likely bundled).
func looksMinified(b []byte) bool {
	lines := strings.Count(string(b), "\n") + 1
	return len(b)/lines > 400
}

// repoBlob concatenates all files (with path headers) for the Pass-1 read.
func repoBlob(chunks []*FileChunk) string {
	var sb strings.Builder
	for _, c := range chunks {
		sb.WriteString("=== FILE: ")
		sb.WriteString(c.Path)
		sb.WriteString(" ===\n")
		sb.WriteString(c.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
