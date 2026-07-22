package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/darron/dbrain/internal/app"
)

func main() {
	if err := app.Run(context.Background(), os.Args[1:]); err != nil {
		var exitErr *app.ExitError
		if errors.As(err, &exitErr) {
			if !exitErr.Silent && exitErr.Err != nil {
				fmt.Fprintln(os.Stderr, exitErr.Err)
			}
			os.Exit(exitErr.Code)
		}
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(os.Stderr, "interrupted")
			os.Exit(130)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
