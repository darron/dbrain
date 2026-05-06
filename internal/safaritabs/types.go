package safaritabs

import "time"

const sourceType = "safari_tab"

type ProgressFunc func(ProgressEvent)

type ProgressEvent struct {
	Phase     string `json:"phase"`
	Index     int    `json:"index,omitempty"`
	Total     int    `json:"total,omitempty"`
	SourceKey string `json:"source_key,omitempty"`
	Title     string `json:"title,omitempty"`
	URL       string `json:"url,omitempty"`
	Status    string `json:"status,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Rendered  bool   `json:"rendered,omitempty"`
}

type Stats struct {
	SourceDBPath  string       `json:"source_db_path"`
	Snapshot      SnapshotInfo `json:"snapshot"`
	DevicesSeen   int          `json:"devices_seen"`
	DeviceName    string       `json:"device_name"`
	DeviceUUID    string       `json:"device_uuid"`
	TabsSeen      int          `json:"tabs_seen"`
	TabsMatched   int          `json:"tabs_matched"`
	TabsImported  int          `json:"tabs_imported"`
	TabsCreated   int          `json:"tabs_created"`
	TabsUpdated   int          `json:"tabs_updated"`
	TabsUnchanged int          `json:"tabs_unchanged"`
	TabsRendered  int          `json:"tabs_rendered"`
	TabsSkipped   int          `json:"tabs_skipped"`
	LinksFound    int          `json:"links_found"`
	SampleTitles  []string     `json:"sample_titles,omitempty"`
	DryRun        bool         `json:"dry_run"`
	Applied       bool         `json:"applied"`
	Errors        int          `json:"errors"`
}

type Device struct {
	UUID                string    `json:"uuid"`
	Name                string    `json:"name"`
	TypeIdentifier      string    `json:"type_identifier"`
	HasDuplicateName    bool      `json:"has_duplicate_name"`
	IsEphemeral         bool      `json:"is_ephemeral"`
	LastModified        time.Time `json:"last_modified"`
	TabCount            int       `json:"tab_count"`
	OldestTabLastViewed time.Time `json:"oldest_tab_last_viewed,omitempty"`
	NewestTabLastViewed time.Time `json:"newest_tab_last_viewed,omitempty"`
}

type Tab struct {
	UUID            string    `json:"uuid"`
	DeviceUUID      string    `json:"device_uuid"`
	DeviceName      string    `json:"device_name"`
	DeviceType      string    `json:"device_type"`
	Title           string    `json:"title"`
	URL             string    `json:"url"`
	IsShowingReader bool      `json:"is_showing_reader"`
	IsPinned        bool      `json:"is_pinned"`
	SceneID         string    `json:"scene_id,omitempty"`
	LastViewed      time.Time `json:"last_viewed,omitempty"`
}
