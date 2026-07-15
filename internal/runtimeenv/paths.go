package runtimeenv

import "strings"

func configValuePaths(key string) [][]string {
	key = strings.TrimSpace(key)
	lowerKey := strings.ToLower(key)
	paths := [][]string{
		{key},
		{"env", key},
		{lowerKey},
		{"env", lowerKey},
	}
	if strings.EqualFold(key, "DBRAIN_USER_AGENT") {
		paths = append(paths, []string{"http", "user_agent"})
	}
	if short, ok := strings.CutPrefix(key, "DBRAIN_APPLE_NOTES_"); ok {
		paths = append(paths, []string{"apple_notes", strings.ToLower(strings.Trim(short, "_"))})
	}
	if short, ok := strings.CutPrefix(key, "DBRAIN_SAFARI_TABS_"); ok {
		paths = append(paths, []string{"safari_tabs", strings.ToLower(strings.Trim(short, "_"))})
	}
	if short, ok := strings.CutPrefix(key, "DBRAIN_SCHEDULER_SYNC_ALL_"); ok {
		paths = append(paths, []string{"scheduler", "sync_all", strings.ToLower(strings.Trim(short, "_"))})
	}
	if short, ok := strings.CutPrefix(key, "DBRAIN_SCHEDULER_SQLITE_ARCHIVE_"); ok {
		paths = append(paths, []string{"scheduler", "sqlite_archive", strings.ToLower(strings.Trim(short, "_"))})
	}
	if short, ok := strings.CutPrefix(key, "DBRAIN_AUDIT_TIMEOUT_"); ok {
		paths = append(paths, []string{"audit", "timeouts", strings.ToLower(strings.Trim(short, "_"))})
	}
	if short, ok := strings.CutPrefix(key, "DBRAIN_AUDIT_ALERT_"); ok {
		paths = append(paths, []string{"audit", "alert", strings.ToLower(strings.Trim(short, "_"))})
	} else if short, ok := strings.CutPrefix(key, "DBRAIN_AUDIT_"); ok {
		paths = append(paths, []string{"audit", strings.ToLower(strings.Trim(short, "_"))})
	}
	if short, ok := strings.CutPrefix(key, "DBRAIN_SYNC_ALL_IMPORT_"); ok {
		paths = append(paths, []string{"sync_all", "imports", strings.ToLower(strings.Trim(short, "_"))})
	} else if short, ok := strings.CutPrefix(key, "DBRAIN_SYNC_ALL_"); ok {
		paths = append(paths, []string{"sync_all", strings.ToLower(strings.Trim(short, "_"))})
	}

	for _, prefix := range []string{"DBRAIN_", "OPENROUTER_", "OLLAMA_", "SUMMARIZE_", "AWS_"} {
		if short, ok := strings.CutPrefix(key, prefix); ok {
			paths = append(paths, shortKeyPaths(short)...)
		}
	}
	paths = append(paths, shortKeyPaths(key)...)
	return paths
}

func shortKeyPaths(key string) [][]string {
	key = strings.ToLower(strings.Trim(key, "_"))
	if key == "" {
		return nil
	}

	paths := [][]string{{key}}
	parts := strings.Split(key, "_")
	if len(parts) > 1 {
		paths = append(paths,
			[]string{parts[0], strings.Join(parts[1:], "_")},
		)
	}
	if len(parts) > 2 {
		paths = append(paths, []string{parts[0], parts[1], strings.Join(parts[2:], "_")})
	}
	if len(parts) > 1 {
		paths = append(paths, parts)
	}
	return paths
}
