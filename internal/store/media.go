package store

const mediaSelectColumns = `
	id, remote_url, media_type, mime_type, width, height, byte_size, content_hash,
	download_status, download_error, local_path,
	archive_provider, archive_bucket, archive_key, archive_url, archive_etag, archive_status, archive_error,
	discovered_at, downloaded_at, archived_at, local_pruned_at, updated_at`
