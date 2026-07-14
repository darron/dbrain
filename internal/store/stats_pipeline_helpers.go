package store

import "sort"

func buildPipelineStageRows(total []CountBucket, current []CountBucket, pending []CountBucket, blocked []CountBucket, terminal []CountBucket) []PipelineStageRow {
	if len(total) == 0 {
		return nil
	}

	currentByKind := countBucketMap(current)
	pendingByKind := countBucketMap(pending)
	blockedByKind := countBucketMap(blocked)
	terminalByKind := countBucketMap(terminal)

	rows := make([]PipelineStageRow, 0, len(total)+1)
	for _, bucket := range total {
		row := PipelineStageRow{
			Kind:     bucket.Key,
			Total:    bucket.Count,
			Current:  currentByKind[bucket.Key],
			Pending:  pendingByKind[bucket.Key],
			Blocked:  blockedByKind[bucket.Key],
			Terminal: terminalByKind[bucket.Key],
		}
		finalizePipelineStageRow(&row)
		rows = append(rows, row)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Total == rows[j].Total {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Total > rows[j].Total
	})

	return append([]PipelineStageRow{aggregatePipelineStageRows(rows)}, rows...)
}

func appendPipelineStageRow(rows []PipelineStageRow, extra PipelineStageRow) []PipelineStageRow {
	if extra.Kind == "" {
		return rows
	}
	if len(rows) == 0 {
		return []PipelineStageRow{extra}
	}

	out := append([]PipelineStageRow(nil), rows...)
	if out[0].Kind == "ALL" {
		detailRows := append([]PipelineStageRow(nil), out[1:]...)
		detailRows = append(detailRows, extra)
		sort.SliceStable(detailRows, func(i, j int) bool {
			if detailRows[i].Total == detailRows[j].Total {
				return detailRows[i].Kind < detailRows[j].Kind
			}
			return detailRows[i].Total > detailRows[j].Total
		})
		return append([]PipelineStageRow{aggregatePipelineStageRows(detailRows)}, detailRows...)
	}

	out = append(out, extra)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total == out[j].Total {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Total > out[j].Total
	})
	return out
}

func aggregatePipelineStageRows(rows []PipelineStageRow) PipelineStageRow {
	total := PipelineStageRow{Kind: pipelineKindAll}
	for _, row := range rows {
		total.Total += row.Total
		total.Current += row.Current
		total.Pending += row.Pending
		total.Blocked += row.Blocked
		total.Terminal += row.Terminal
		total.Failed += row.Failed
		total.Unknown += row.Unknown
	}
	finalizePipelineStageRow(&total)
	return total
}

func finalizePipelineStageRow(row *PipelineStageRow) {
	if row == nil {
		return
	}
	known := row.Current + row.Pending + row.Blocked + row.Terminal + row.Failed + row.Unknown
	if row.Total > known {
		row.Unknown += row.Total - known
	}
	row.PartitionValid = row.Total == row.Current+row.Pending+row.Blocked+row.Terminal+row.Failed+row.Unknown
	if row.Total > 0 {
		row.PercentCurrent = (float64(row.Current) / float64(row.Total)) * 100
	}
}

func countBucketMap(buckets []CountBucket) map[string]int {
	out := make(map[string]int, len(buckets))
	for _, bucket := range buckets {
		out[bucket.Key] = bucket.Count
	}
	return out
}
