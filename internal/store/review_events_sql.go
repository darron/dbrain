package store

import "github.com/darron/dbrain/internal/model"

const reviewEventSQLColumns = `
	event_kind, event_at, entity_kind, entity_id, entity_key, event_stage,
	source_type, title, url, note_path, summary, tags, status, message`

const reviewEventsUnionQuery = `
	SELECT
		'` + ReviewEventKindItemImported + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', i.imported_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'imported' AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		'' AS summary,
		trim(coalesce(i.categories, '') || ',' || coalesce(i.user_tags, ''), ',') AS tags,
		'ok' AS status,
		'' AS message
	FROM items i
	WHERE i.imported_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', i.imported_at) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindItemUpdated + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', fe.last_changed_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		'updated' AS event_stage,
		i.source_type,
		coalesce(nullif(i.title, ''), fe.title) AS title,
		coalesce(nullif(i.canonical_url, ''), fe.link) AS url,
		i.note_path,
		'' AS summary,
		trim(coalesce(i.categories, '') || ',' || coalesce(i.user_tags, ''), ',') AS tags,
		'ok' AS status,
		'' AS message
	FROM feed_entries fe
	JOIN items i ON i.id = fe.item_id
	WHERE fe.version > 1
		AND fe.last_changed_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', fe.last_changed_at) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindSourceCreated + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', s.created_at) AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'created' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		'' AS summary,
		s.user_tags AS tags,
		'ok' AS status,
		'' AS message
	FROM sources s
	WHERE s.created_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', s.created_at) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindSourceExtracted + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', s.extracted_at) AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'extracted' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		'' AS summary,
		s.user_tags AS tags,
		s.extract_status AS status,
		'' AS message
	FROM sources s
	WHERE s.extract_status IN ('` + model.SourceExtractStatusOK + `', '` + model.SourceExtractStatusEmpty + `')
		AND s.extracted_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', s.extracted_at) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindSourceSummarized + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', s.summarized_at) AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'summarized' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		s.summary_text AS summary,
		s.user_tags AS tags,
		s.summary_status AS status,
		'' AS message
	FROM sources s
	WHERE s.summary_status = '` + model.SourceSummaryStatusOK + `'
		AND s.summarized_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', s.summarized_at) IS NOT NULL

	UNION ALL

	SELECT
		CASE ie.role
			WHEN '` + model.ItemEnrichmentRoleSummary + `' THEN '` + ReviewEventKindItemSummarized + `'
			WHEN '` + model.ItemEnrichmentRoleXMediaTranscript + `' THEN '` + ReviewEventKindXMediaTranscribed + `'
			ELSE '` + ReviewEventKindXPhotoOCRed + `'
		END AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', ie.completed_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		CASE ie.role
			WHEN '` + model.ItemEnrichmentRoleXMediaTranscript + `' THEN 'transcript'
			ELSE ie.role
		END AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		ie.text AS summary,
		trim(coalesce(i.categories, '') || ',' || coalesce(i.user_tags, ''), ',') AS tags,
		ie.status,
		'' AS message
	FROM item_enrichments ie
	JOIN items i ON i.id = ie.item_id
	WHERE ie.role IN ('` + model.ItemEnrichmentRoleSummary + `', '` + model.ItemEnrichmentRoleOCR + `', '` + model.ItemEnrichmentRoleXMediaTranscript + `')
		AND ie.status = '` + model.ItemSummaryStatusOK + `'
		AND ie.completed_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', ie.completed_at) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindSourceFailed + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', s.updated_at) AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'summary' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		'' AS summary,
		s.user_tags AS tags,
		s.summary_status AS status,
		s.summary_error AS message
	FROM sources s
	WHERE s.summary_status = '` + model.SourceSummaryStatusError + `'
		AND s.updated_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', s.updated_at) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindSourceFailed + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', coalesce(nullif(s.extract_last_failed_at, ''), s.updated_at)) AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'extract' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		'' AS summary,
		s.user_tags AS tags,
		s.extract_status AS status,
		s.extract_error AS message
	FROM sources s
	WHERE s.extract_status IN ('` + model.SourceExtractStatusError + `', '` + model.SourceExtractStatusDead + `', '` + model.SourceExtractStatusGone + `')
		AND coalesce(nullif(s.extract_last_failed_at, ''), s.updated_at) != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', coalesce(nullif(s.extract_last_failed_at, ''), s.updated_at)) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindItemEnrichmentFailed + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', ie.updated_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		CASE ie.role
			WHEN '` + model.ItemEnrichmentRoleXMediaTranscript + `' THEN 'transcript'
			ELSE ie.role
		END AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		ie.text AS summary,
		trim(coalesce(i.categories, '') || ',' || coalesce(i.user_tags, ''), ',') AS tags,
		ie.status,
		ie.error AS message
	FROM item_enrichments ie
	JOIN items i ON i.id = ie.item_id
	WHERE ie.role IN ('` + model.ItemEnrichmentRoleSummary + `', '` + model.ItemEnrichmentRoleOCR + `', '` + model.ItemEnrichmentRoleXMediaTranscript + `')
		AND ie.status = '` + model.ItemSummaryStatusError + `'
		AND ie.updated_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', ie.updated_at) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindBlocked + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', coalesce(nullif(s.summarized_at, ''), s.updated_at)) AS event_at,
		'source' AS entity_kind,
		s.id AS entity_id,
		s.source_key AS entity_key,
		'summary' AS event_stage,
		s.source_type,
		s.title,
		s.canonical_url AS url,
		s.note_path,
		s.summary_text AS summary,
		s.user_tags AS tags,
		s.summary_status AS status,
		s.summary_error AS message
	FROM sources s
	WHERE s.summary_status IN ('` + model.SourceSummaryStatusBlocked + `', '` + model.SourceSummaryStatusSkipped + `')
		AND coalesce(nullif(s.summarized_at, ''), s.updated_at) != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', coalesce(nullif(s.summarized_at, ''), s.updated_at)) IS NOT NULL

	UNION ALL

	SELECT
		'` + ReviewEventKindBlocked + `' AS event_kind,
		strftime('%Y-%m-%dT%H:%M:%SZ', ie.updated_at) AS event_at,
		'item' AS entity_kind,
		i.id AS entity_id,
		i.source_key AS entity_key,
		ie.role AS event_stage,
		i.source_type,
		i.title,
		i.canonical_url AS url,
		i.note_path,
		ie.text AS summary,
		trim(coalesce(i.categories, '') || ',' || coalesce(i.user_tags, ''), ',') AS tags,
		ie.status,
		ie.error AS message
	FROM item_enrichments ie
	JOIN items i ON i.id = ie.item_id
	WHERE ie.role IN ('` + model.ItemEnrichmentRoleSummary + `', '` + model.ItemEnrichmentRoleOCR + `')
		AND ie.status IN ('` + model.ItemSummaryStatusBlocked + `', '` + model.ItemSummaryStatusSkipped + `')
		AND ie.updated_at != ''
		AND strftime('%Y-%m-%dT%H:%M:%SZ', ie.updated_at) IS NOT NULL`
