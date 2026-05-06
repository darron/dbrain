package applenotes

func emitProgress(opts Options, event ProgressEvent) {
	if opts.Progress == nil {
		return
	}
	opts.Progress(event)
}
