package syncjob

import (
	"github.com/darron/dbrain/internal/applenotes"
	"github.com/darron/dbrain/internal/bskyapi"
	"github.com/darron/dbrain/internal/feedimport"
	"github.com/darron/dbrain/internal/githubimport"
	"github.com/darron/dbrain/internal/itemcategorize"
	"github.com/darron/dbrain/internal/linkextract"
	"github.com/darron/dbrain/internal/mediaarchive"
	"github.com/darron/dbrain/internal/okf"
	"github.com/darron/dbrain/internal/safaritabs"
	"github.com/darron/dbrain/internal/sourceenrich"
	"github.com/darron/dbrain/internal/worker"
	"github.com/darron/dbrain/internal/xapi"
	"github.com/darron/dbrain/internal/xmediatranscribe"
	"github.com/darron/dbrain/internal/xphotoocr"
	"github.com/darron/dbrain/internal/youtubeimport"
)

var (
	runXBookmarkImport       = xapi.RunBookmarks
	runBlueskyBookmarkImport = bskyapi.RunBookmarks
	runXHydrate              = xapi.Run
	runXMediaStage           = xmediatranscribe.Run
	runXPhotoOCRStage        = xphotoocr.Run
	runLinkExtract           = linkextract.Run
	runGitHubImport          = githubimport.Run
	runYouTubeImport         = youtubeimport.Run
	runSourceWorker          = worker.RunSources
	runSourceEnrichPending   = sourceenrich.RunPending
	runMediaArchive          = mediaarchive.Run
	runItemCategorize        = itemcategorize.Batch
	runSourceCategorize      = itemcategorize.BatchSources
	runAppleNotesImport      = applenotes.Run
	runSafariTabsImport      = safaritabs.Run
	runFeedImport            = feedimport.Run
	runOKFExport             = okf.Export
)
