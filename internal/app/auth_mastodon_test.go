package app

import "testing"

func TestRootCommandIncludesMastodonAuthCommands(t *testing.T) {
	root := NewRootCommand()
	for _, path := range [][]string{
		{"auth", "mastodon", "login"},
		{"auth", "mastodon", "status"},
		{"auth", "mastodon", "logout"},
	} {
		command := root
		for _, name := range path {
			var next = command
			for _, candidate := range command.Commands() {
				if candidate.Name() == name {
					next = candidate
					break
				}
			}
			if next == command {
				t.Fatalf("missing command %v at %q", path, name)
			}
			command = next
		}
		if command == nil || command.Name() != path[len(path)-1] {
			t.Fatalf("find %v = %#v", path, command)
		}
	}
}
