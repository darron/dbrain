package syncjob

import (
	"strings"
	"time"
)

func normalizeOptions(opts Options) Options {
	if strings.TrimSpace(opts.Browser) == "" {
		opts.Browser = "chrome"
	}
	if strings.TrimSpace(opts.Length) == "" {
		opts.Length = "medium"
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 5 * time.Minute
	}
	if opts.XLimit <= 0 {
		opts.XLimit = 100
	}
	if opts.XTimeout <= 0 {
		opts.XTimeout = 30 * time.Second
	}
	if opts.XMediaLimit <= 0 {
		opts.XMediaLimit = opts.XLimit
	}
	if opts.XPhotoOCRLimit <= 0 {
		opts.XPhotoOCRLimit = opts.XLimit
	}
	if opts.XConcurrency <= 0 {
		opts.XConcurrency = 4
	}
	if opts.LinkDiscoverLimit <= 0 {
		opts.LinkDiscoverLimit = 500
	}
	if opts.LinkLimit <= 0 {
		opts.LinkLimit = 100
	}
	if opts.LinkConcurrency <= 0 {
		opts.LinkConcurrency = 4
	}
	if opts.FeedLimit <= 0 {
		opts.FeedLimit = 100
	}
	if opts.YouTubeLimit <= 0 {
		opts.YouTubeLimit = 50
	}
	if opts.SourceLimit <= 0 {
		opts.SourceLimit = 100
	}
	if opts.SourceConcurrency <= 0 {
		opts.SourceConcurrency = 4
	}
	if opts.ArchiveMediaLimit <= 0 {
		opts.ArchiveMediaLimit = 5000
	}
	return opts
}
