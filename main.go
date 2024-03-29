package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/c-bata/go-prompt"
	"github.com/fatih/color"
	"github.com/rebel-l/mp3sync/config"
	"github.com/rebel-l/mp3sync/filesync"
	"github.com/rebel-l/mp3sync/filter"
	"github.com/rebel-l/mp3sync/transform"
)

const (
	configFile        = "config.json"
	logPath           = "logs"
	logFileNameFormat = "20060102-150405"
)

var (
	errPathNotExisting = errors.New("path does not exist")
	errWriteLog        = errors.New("failed to write log file")
	errAbortedByUser   = errors.New("aborted by user")
	errFormat          = color.New(color.FgRed)
	description        = color.New(color.FgGreen)  // nolint: gochecknoglobals
	listFormat         = color.New(color.FgHiBlue) // nolint: gochecknoglobals
)

func main() {
	title := color.New(color.Bold, color.FgGreen)
	_, _ = title.Println("MP3 sync started ...")
	fmt.Println()

	info := color.New(color.FgYellow)

	conf, err := config.Load(filepath.Join(".", configFile))
	if err != nil {
		_, _ = errFormat.Printf("failed to load config: %v", err)
		return
	}

	_, _ = description.Printf("Source: %s\n", info.Sprint(conf.Source))
	_, _ = description.Printf("Destination: %s\n", info.Sprint(conf.Destination))

	fmt.Println()

	if err := do(conf); err != nil {
		fmt.Println()

		_, _ = errFormat.Printf("MP3 sync finished with error: %v\n", err)
	} else {
		fmt.Println()

		_, _ = title.Println("MP3 sync finished successful!")
	}
}

func do(conf *config.Config) error {
	// 1. read file list from source (incl. filter) and destination (excl. filter)
	sourceChannel := make(chan FileChannel)
	destinationChannel := make(chan FileChannel)
	go readFileList(sourceChannel, conf.Source, conf.Filter)
	go readFileList(destinationChannel, conf.Destination, nil)

	sourceResult := <-sourceChannel
	if sourceResult.Err != nil {
		return sourceResult.Err
	}

	destinationResult := <-destinationChannel
	if destinationResult.Err != nil {
		return destinationResult.Err
	}

	fmt.Println()

	var globErr bool

	// 2. filter & transform source
	_, _ = description.Println("Filter & transform files to be synced ...")
	start := time.Now()

	wl, _ := conf.Filter.MP3Tag(filter.KeyWhitelist)
	bl, _ := conf.Filter.MP3Tag(filter.KeyBlacklist)

	transformedSource, errs := transform.Do(sourceResult.Files, conf.Destination, conf.Source, wl, bl)
	if len(errs) > 0 {
		globErr = true

		if err := showAndLogErrors(errs); err != nil {
			return err
		}
	}

	duration(start, time.Now(), fmt.Sprintf("%d files filtered and transformed result in %d files", len(sourceResult.Files), len(transformedSource)))
	fmt.Println()

	// 3. diff file sizes + source / destination and set operations: copy / delete
	syncFiles := diff(transformedSource, destinationResult.Files)

	// 4. ask to list diff?
	listDiff(syncFiles)

	// 5. show disk space
	ds, err := filesync.CalculateDiskSpace(syncFiles, conf.Destination)
	if err != nil {
		return err
	}

	_, _ = listFormat.Printf("Free Disk Space: %s\n", ds.Free)
	_, _ = listFormat.Printf("Disk Space Needed: %s\n", ds.Needed)
	_, _ = listFormat.Printf("Disk Space Left: %s\n", ds.Left)

	fmt.Println()

	// 6. run operations
	t := prompt.Input("Start Sync? [Y/n] ", func(d prompt.Document) []prompt.Suggest {
		return prompt.FilterHasPrefix([]prompt.Suggest{}, d.GetWordBeforeCursor(), true)
	})

	if strings.ToLower(t) == "n" {
		return errAbortedByUser
	}

	_, _ = description.Print("Sync files: ")

	start = time.Now()

	errs = filesync.Do(syncFiles)

	if len(errs) > 1 {
		if errors.Is(errs[0], errAbortedByUser) {
			return errs[0]
		}

		globErr = true

		logFileName, err := logErrors(errs)
		if err != nil {
			return err
		}

		_, _ = errFormat.Printf("found %d errors\n", len(errs))
		_, _ = errFormat.Printf("logged errors in file %s\n", logFileName)
	}

	if globErr {
		return errors.New("see log for more details")
	}

	duration(start, time.Now(), fmt.Sprintf("%d files synced", len(syncFiles)))

	return nil
}

func duration(start, finish time.Time, msg string) {
	_, _ = description.Printf("%s in %s\n", msg, finish.Sub(start))
}

func showAndLogErrors(errs []error) error {
	logFileName, err := logErrors(errs)
	if err != nil {
		return err
	}

	return showErrors(logFileName, errs)
}

func showErrors(logFileName string, errs []error) error {
	_, _ = errFormat.Printf("found %d errors\n", len(errs))

	t := prompt.Input("Continue (errored files will be skipped)? [Y/n/s = show files] ",
		func(d prompt.Document) []prompt.Suggest {
			return prompt.FilterHasPrefix([]prompt.Suggest{}, d.GetWordBeforeCursor(), true)
		},
	)

	_, _ = errFormat.Printf("logged errors in file %s\n", logFileName)

	switch strings.ToLower(t) {
	case "n":
		return errAbortedByUser
	case "s":
		for _, e := range errs {
			_, _ = errFormat.Println(e.Error())
		}
	}

	return nil
}

func logErrors(errs []error) (string, error) {
	logFileName := filepath.Join(".", logPath, fmt.Sprintf("%s.log", time.Now().Format(logFileNameFormat)))

	logFile, err := os.OpenFile(logFileName, os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return logFileName, fmt.Errorf("%w: %v", errWriteLog, err)
	}

	defer func() {
		_ = logFile.Close()
	}()

	for _, e := range errs {
		if _, err := logFile.WriteString(e.Error() + "\n"); err != nil {
			return logFileName, fmt.Errorf("%w: %v", errWriteLog, err)
		}
	}

	return logFileName, nil
}

// TODO:
// 2. delete not matching files
// 3. activate all linters
// 4. Documentation / Badges: licence, goreport, issues, releases
// 5. Tests / Badges: build, coverage
