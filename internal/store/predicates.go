package store

const xItemSourceTypeWhere = "(source_type = 'x_bookmark' OR source_type = 'x_quote')"
const linkDiscoveryItemSourceTypeWhere = "(source_type = 'x_bookmark' OR source_type = 'x_quote' OR source_type = 'apple_note' OR source_type = 'safari_tab')"
const xTopLevelMediaObjectsWhere = `(json_valid(x_post_json) AND json_extract(x_post_json, '$.snapshot.media_objects[0].type') IS NOT NULL)`
const xQuotedPostRepairWhere = `((x_post_json LIKE '%"quoted_tweet"%' OR x_post_json LIKE '%"quoted_status_result"%' OR x_post_json LIKE '%"quoted_post"%')
	AND NOT EXISTS (
		SELECT 1
		FROM item_item_links q
		WHERE q.parent_item_id = items.id
			AND q.link_kind = 'quoted_post'
	))`
const xQuoteDirectHydrationRepairWhere = `(source_type = 'x_quote'
	AND x_post_status = 'ok_graphql'
	AND x_post_json NOT LIKE '%"tweetResult"%')`
const xNoteTweetLinkRepairWhere = `(json_valid(x_post_json)
	AND EXISTS (
		SELECT 1
		FROM json_tree(
			CASE WHEN json_valid(items.x_post_json) THEN items.x_post_json ELSE '{}' END,
			'$.raw.data.tweetResult.result.note_tweet.note_tweet_results.result.entity_set.urls'
		) note_url
		WHERE note_url.key = 'expanded_url'
			AND COALESCE(note_url.value, '') != ''
			AND NOT EXISTS (
				SELECT 1
				FROM json_each(CASE WHEN json_valid(items.links_json) THEN items.links_json ELSE '[]' END) existing_link
				WHERE existing_link.value = note_url.value
			)
	))`
const xMediaHydrationRepairWhere = `(` + xTopLevelMediaObjectsWhere + `
	AND (
		NOT EXISTS (
			SELECT 1
			FROM item_media_links l
			WHERE l.item_id = items.id
		)
		OR EXISTS (
			SELECT 1
			FROM item_media_links l
			JOIN media_assets a ON a.id = l.media_asset_id
			WHERE l.item_id = items.id
				AND (
					a.download_status = ''
					OR a.download_status = 'pending'
					OR a.download_status = 'error'
				)
		)
		OR EXISTS (
			SELECT 1
			FROM item_media_links l
			JOIN media_assets a ON a.id = l.media_asset_id
			WHERE l.item_id = items.id
				AND a.download_status = 'downloaded'
				AND a.media_type IN ('video', 'animated_gif')
				AND (
					a.local_path GLOB '*.jpg'
					OR a.local_path GLOB '*.jpeg'
					OR a.local_path GLOB '*.png'
					OR a.local_path GLOB '*.webp'
					OR a.remote_url LIKE 'https://pbs.twimg.com/%'
				)
		)
	))`
const xHydrationRepairWhere = `(` + xQuotedPostRepairWhere + `
	OR ` + xQuoteDirectHydrationRepairWhere + `
	OR ` + xNoteTweetLinkRepairWhere + `)`
const xHydrationCandidateWhere = `(
	x_post_status = ''
	OR x_post_status = 'api_error'
	OR x_post_status = 'error'
	OR x_post_status = 'rate_limited'
	OR (
		x_post_status LIKE 'ok_%'
		AND (
			` + xMediaHydrationRepairWhere + `
			OR ` + xHydrationRepairWhere + `
		)
	)
)`
