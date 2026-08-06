package app

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func resolveSyncAllFlags(rootDir string, flags syncAllFlags, overrides ...syncAllFlagOverrides) (syncAllFlags, error) {
	explicit := syncAllFlagOverrides{}
	if len(overrides) > 0 {
		explicit = overrides[0]
	}
	flags = applySyncAllImportPolicy(flags, syncAllImportPolicyFromRuntime(rootDir), explicit)
	flags.semanticGC = firstEnvBool(rootDir, "DBRAIN_SYNC_ALL_SEMANTIC_GC")
	if !explicit.browser && strings.TrimSpace(flags.browser) == "" {
		flags.browser = firstNonEmptyEnv(rootDir, "DBRAIN_SYNC_ALL_BROWSER")
	}
	if !explicit.profile && strings.TrimSpace(flags.profile) == "" {
		flags.profile = firstNonEmptyEnv(rootDir, "DBRAIN_SYNC_ALL_PROFILE")
	}
	if !flags.archiveMedia {
		flags.archiveMedia = firstEnvBool(rootDir, "DBRAIN_AUTO_ARCHIVE_MEDIA", "DBRAIN_ARCHIVE_AUTO")
	}
	if !flags.okfExport {
		flags.okfExport = firstEnvBool(rootDir, "DBRAIN_OKF_EXPORT_ENABLED", "DBRAIN_SYNC_OKF_EXPORT")
	}
	if !explicit.skipXPhotoOCR && !flags.skipXPhotoOCR {
		flags.skipXPhotoOCR = firstEnvBool(rootDir, "DBRAIN_SCHEDULER_SYNC_ALL_SKIP_X_PHOTO_OCR")
	}
	if !explicit.skipCategorize && !flags.skipCategorize {
		flags.skipCategorize = firstEnvBool(rootDir, "DBRAIN_SCHEDULER_SYNC_ALL_SKIP_CATEGORIZE")
	}
	if strings.TrimSpace(flags.appleNotesDBPath) == "" {
		flags.appleNotesDBPath = firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_DB_PATH")
	}
	if len(flags.appleNotesExcludeFolders) == 0 {
		flags.appleNotesExcludeFolders = firstEnvList(rootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_FOLDERS")
	}
	if len(flags.appleNotesExcludeAccounts) == 0 {
		flags.appleNotesExcludeAccounts = firstEnvList(rootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_ACCOUNTS")
	}
	if !flags.appleNotesExcludeShared {
		flags.appleNotesExcludeShared = firstEnvBool(rootDir, "DBRAIN_APPLE_NOTES_EXCLUDE_SHARED")
	}
	if !flags.appleNotesSkipAttachments {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS"); value != "" {
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_APPLE_NOTES_INDEX_ATTACHMENTS: %q", value)
			}
			flags.appleNotesSkipAttachments = !parsed
		}
		if firstEnvBool(rootDir, "DBRAIN_APPLE_NOTES_SKIP_ATTACHMENTS") {
			flags.appleNotesSkipAttachments = true
		}
	}
	if !flags.appleNotesSkipAttachmentOCR {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_ATTACHMENT_OCR"); value != "" {
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_APPLE_NOTES_ATTACHMENT_OCR: %q", value)
			}
			flags.appleNotesSkipAttachmentOCR = !parsed
		}
		if firstEnvBool(rootDir, "DBRAIN_APPLE_NOTES_SKIP_ATTACHMENT_OCR") {
			flags.appleNotesSkipAttachmentOCR = true
		}
	}
	if flags.appleNotesAttachmentMaxBytes <= 0 {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES"); value != "" {
			parsed, parseErr := strconv.ParseInt(value, 10, 64)
			if parseErr != nil || parsed < 0 {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_APPLE_NOTES_ATTACHMENT_MAX_BYTES: %q", value)
			}
			flags.appleNotesAttachmentMaxBytes = parsed
		}
	}
	if strings.TrimSpace(flags.appleNotesTesseractBinary) == "" {
		flags.appleNotesTesseractBinary = firstNonEmptyEnv(rootDir, "DBRAIN_APPLE_NOTES_TESSERACT_BINARY")
	}
	if strings.TrimSpace(flags.safariTabsDBPath) == "" {
		flags.safariTabsDBPath = firstNonEmptyEnv(rootDir, "DBRAIN_SAFARI_TABS_DB_PATH")
	}
	if strings.TrimSpace(flags.safariTabsDevice) == "" {
		flags.safariTabsDevice = firstNonEmptyEnv(rootDir, "DBRAIN_SAFARI_TABS_DEVICE")
	}
	if flags.safariTabsLimit <= 0 {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_SAFARI_TABS_LIMIT"); value != "" {
			parsed, parseErr := strconv.Atoi(value)
			if parseErr != nil || parsed < 0 {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_SAFARI_TABS_LIMIT: %q", value)
			}
			flags.safariTabsLimit = parsed
		}
	}
	if flags.safariTabsOlderThan <= 0 {
		if value := firstNonEmptyEnv(rootDir, "DBRAIN_SAFARI_TABS_OLDER_THAN"); value != "" {
			parsed, parseErr := time.ParseDuration(value)
			if parseErr != nil || parsed < 0 {
				return syncAllFlags{}, fmt.Errorf("parse DBRAIN_SAFARI_TABS_OLDER_THAN: %q", value)
			}
			flags.safariTabsOlderThan = parsed
		}
	}
	return flags, nil
}
