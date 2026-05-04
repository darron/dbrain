package entities

import "path/filepath"

func NoteRelativePath(kind Kind, key string) string {
	return filepath.ToSlash(filepath.Join("entities", string(kind), entitySlug(key)+".md"))
}
