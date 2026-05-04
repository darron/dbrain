package vault

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/darron/dbrain/internal/model"
)

func writeItemMediaSection(b *strings.Builder, mediaRefs []model.ItemMediaRef, opts RenderOptions) {
	if len(mediaRefs) == 0 {
		return
	}

	b.WriteString("\n## Media\n\n")
	for _, media := range mediaRefs {
		b.WriteString("### ")
		b.WriteString(mediaHeading(media))
		b.WriteString("\n\n")
		proxyURL := archivedMediaProxyURL(opts, media)
		switch {
		case media.DownloadStatus == "downloaded" && strings.TrimSpace(media.LocalPath) != "" && media.LocalPrunedAt.IsZero():
			b.WriteString("![[")
			b.WriteString(media.LocalPath)
			b.WriteString("]]\n\n")
		case strings.TrimSpace(media.ArchiveURL) != "" && strings.TrimSpace(media.ArchiveStatus) == "archived":
			if media.MediaType == "photo" {
				writeArchivedImageEmbed(b, media.ArchiveURL)
			} else if mediaIsVideoLike(media) {
				writeArchivedVideoEmbed(b, media.ArchiveURL)
			} else {
				b.WriteString("[Archived media](")
				b.WriteString(media.ArchiveURL)
				b.WriteString(")\n\n")
			}
		case proxyURL != "" && strings.TrimSpace(media.ArchiveStatus) == "archived":
			if media.MediaType == "photo" {
				writeArchivedImageEmbed(b, proxyURL)
			} else if mediaIsVideoLike(media) {
				writeArchivedVideoEmbed(b, proxyURL)
			} else {
				b.WriteString("[Archived media stream](")
				b.WriteString(proxyURL)
				b.WriteString(")\n\n")
			}
		case strings.TrimSpace(media.ArchiveStatus) == "archived":
			b.WriteString("Archived remotely. No anonymous media URL is configured.\n\n")
		}
		b.WriteString("- Status: `")
		b.WriteString(firstNonEmptyString(media.DownloadStatus, "pending"))
		b.WriteString("`\n")
		if media.RemoteURL != "" {
			b.WriteString("- Remote URL: ")
			b.WriteString(media.RemoteURL)
			b.WriteString("\n")
		}
		if media.ExpandedURL != "" {
			b.WriteString("- Post Media URL: ")
			b.WriteString(media.ExpandedURL)
			b.WriteString("\n")
		}
		if media.Width > 0 && media.Height > 0 {
			b.WriteString("- Dimensions: `")
			_, _ = fmt.Fprintf(b, "%dx%d", media.Width, media.Height)
			b.WriteString("`\n")
		}
		if media.LocalPath != "" {
			b.WriteString("- Local Path: `")
			b.WriteString(media.LocalPath)
			b.WriteString("`\n")
		}
		if media.ArchiveStatus != "" {
			b.WriteString("- Archive Status: `")
			b.WriteString(media.ArchiveStatus)
			b.WriteString("`\n")
		}
		if media.ArchiveProvider != "" {
			b.WriteString("- Archive Provider: `")
			b.WriteString(media.ArchiveProvider)
			b.WriteString("`\n")
		}
		if media.ArchiveBucket != "" {
			b.WriteString("- Archive Bucket: `")
			b.WriteString(media.ArchiveBucket)
			b.WriteString("`\n")
		}
		if media.ArchiveKey != "" {
			b.WriteString("- Archive Key: `")
			b.WriteString(media.ArchiveKey)
			b.WriteString("`\n")
		}
		if media.ArchiveURL != "" {
			b.WriteString("- Archive URL: ")
			b.WriteString(media.ArchiveURL)
			b.WriteString("\n")
		}
		if !media.LocalPrunedAt.IsZero() {
			b.WriteString("- Local Pruned: ")
			b.WriteString(formatTime(media.LocalPrunedAt))
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
}

func archivedMediaProxyURL(opts RenderOptions, media model.ItemMediaRef) string {
	baseURL := strings.TrimSpace(opts.MediaProxyBaseURL)
	if baseURL == "" || media.MediaAssetID <= 0 {
		return ""
	}
	return strings.TrimRight(baseURL, "/") + "/media/asset/" + strconv.FormatInt(media.MediaAssetID, 10)
}

func mediaIsVideoLike(media model.ItemMediaRef) bool {
	switch strings.TrimSpace(media.MediaType) {
	case "video", "animated_gif":
		return true
	default:
		return false
	}
}

func writeArchivedVideoEmbed(b *strings.Builder, url string) {
	b.WriteString("<video controls preload=\"metadata\" src=\"")
	b.WriteString(url)
	b.WriteString("\"></video>\n\n")
	b.WriteString("[Open archived media](")
	b.WriteString(url)
	b.WriteString(")\n\n")
}

func writeArchivedImageEmbed(b *strings.Builder, url string) {
	b.WriteString("![](")
	b.WriteString(url)
	b.WriteString(")\n\n")
	b.WriteString("[Open archived media](")
	b.WriteString(url)
	b.WriteString(")\n\n")
}

func mediaHeading(media model.ItemMediaRef) string {
	label := strings.ReplaceAll(strings.TrimSpace(media.MediaType), "_", " ")
	if label == "" {
		label = "media"
	}
	if len(label) == 1 {
		label = strings.ToUpper(label)
	} else {
		label = strings.ToUpper(label[:1]) + label[1:]
	}
	return label + fmt.Sprintf(" %d", media.Ordinal+1)
}
