package store

import "github.com/darron/dbrain/internal/model"

const sourceActivitySuccessUnionQuery = `
	SELECT
		s.id AS source_id,
		s.source_key,
		s.source_type,
		s.domain,
		'' AS failure_kind,
		s.canonical_url,
		s.title,
		s.note_path,
		'summary_ok' AS event_kind,
		s.summary_status AS status,
		'' AS message,
		s.summarized_at AS event_at
	FROM sources s
	WHERE s.summary_status = '` + model.SourceSummaryStatusOK + `' AND s.summarized_at != ''

	UNION ALL

	SELECT
		s.id AS source_id,
		s.source_key,
		s.source_type,
		s.domain,
		'' AS failure_kind,
		s.canonical_url,
		s.title,
		s.note_path,
		CASE WHEN s.extract_status = '` + model.SourceExtractStatusEmpty + `' THEN 'extract_empty' ELSE 'extract_ok' END AS event_kind,
		s.extract_status AS status,
		'' AS message,
		s.extracted_at AS event_at
	FROM sources s
	WHERE s.extract_status IN ('` + model.SourceExtractStatusOK + `', '` + model.SourceExtractStatusEmpty + `') AND s.extracted_at != ''`

const sourceActivityFailureUnionQuery = `
	SELECT
		s.id AS source_id,
		s.source_key,
		s.source_type,
		s.domain,
		'summary_error' AS failure_kind,
		s.canonical_url,
		s.title,
		s.note_path,
		'summary_error' AS event_kind,
		s.summary_status AS status,
		s.summary_error AS message,
		s.updated_at AS event_at
	FROM sources s
	WHERE s.summary_status = '` + model.SourceSummaryStatusError + `' AND s.updated_at != ''

	UNION ALL

	SELECT
		s.id AS source_id,
		s.source_key,
		s.source_type,
		s.domain,
		COALESCE(NULLIF(s.extract_failure_kind, ''), 'extract_error') AS failure_kind,
		s.canonical_url,
		s.title,
		s.note_path,
		CASE
			WHEN s.extract_status = '` + model.SourceExtractStatusDead + `' THEN 'extract_dead'
			WHEN s.extract_status = '` + model.SourceExtractStatusGone + `' THEN 'extract_gone'
			ELSE 'extract_error'
		END AS event_kind,
		s.extract_status AS status,
		s.extract_error AS message,
		COALESCE(NULLIF(s.extract_last_failed_at, ''), s.updated_at) AS event_at
	FROM sources s
	WHERE s.extract_status IN ('` + model.SourceExtractStatusError + `', '` + model.SourceExtractStatusDead + `', '` + model.SourceExtractStatusGone + `')`

const sourceActivityTrendUnionQuery = `
	SELECT
		s.source_type,
		s.domain,
		'' AS failure_kind,
		s.summary_status AS status,
		'' AS message,
		s.summarized_at AS event_at,
		'success' AS event_class
	FROM sources s
	WHERE s.summary_status = '` + model.SourceSummaryStatusOK + `' AND s.summarized_at != ''

	UNION ALL

	SELECT
		s.source_type,
		s.domain,
		'' AS failure_kind,
		s.extract_status AS status,
		'' AS message,
		s.extracted_at AS event_at,
		'success' AS event_class
	FROM sources s
	WHERE s.extract_status IN ('` + model.SourceExtractStatusOK + `', '` + model.SourceExtractStatusEmpty + `') AND s.extracted_at != ''

	UNION ALL

	SELECT
		s.source_type,
		s.domain,
		'summary_error' AS failure_kind,
		s.summary_status AS status,
		s.summary_error AS message,
		s.updated_at AS event_at,
		'failure' AS event_class
	FROM sources s
	WHERE s.summary_status = '` + model.SourceSummaryStatusError + `' AND s.updated_at != ''

	UNION ALL

	SELECT
		s.source_type,
		s.domain,
		COALESCE(NULLIF(s.extract_failure_kind, ''), 'extract_error') AS failure_kind,
		s.extract_status AS status,
		s.extract_error AS message,
		COALESCE(NULLIF(s.extract_last_failed_at, ''), s.updated_at) AS event_at,
		'failure' AS event_class
	FROM sources s
	WHERE s.extract_status IN ('` + model.SourceExtractStatusError + `', '` + model.SourceExtractStatusDead + `', '` + model.SourceExtractStatusGone + `')`
