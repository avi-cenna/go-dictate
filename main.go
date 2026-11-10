package main

import (
	"context"
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
)

func init() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
}

// appConfig is the global application configuration, populated from defaults + CLI flags.
var appConfig AppConfig

func main() {
	cmd := &cli.Command{
		Name:  "dictation-server",
		Usage: "Audio transcription server using Whisper.Cpp and Groq",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "model",
				Aliases: []string{"m"},
				Usage:   "Path to Whisper model file",
			},
		},
		Commands: []*cli.Command{
			{
				Name:  "serve",
				Usage: "Start the HTTP server",
				Flags: []cli.Flag{
					&cli.IntFlag{
						Name:    "port",
						Aliases: []string{"p"},
						Usage:   "Port to listen on",
						Value:   8080,
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					appConfig = DefaultAppConfig()
					// CLI flag overrides default/env model path
					if cmd.IsSet("model") {
						appConfig.ModelPath = cmd.String("model")
					}
					port := cmd.Int("port")

					transcriber := NewTranscriber(appConfig)

					NewServer(transcriber, port)
					defer CloseServer()

					return StartServer()
				},
			},
			{
				Name:  "download",
				Usage: "Interactively download Whisper.cpp models",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "output",
						Aliases: []string{"o"},
						Usage:   "Output directory for downloaded models",
						Value:   "./models",
					},
				},
				Action: func(ctx context.Context, cmd *cli.Command) error {
					outputDir := cmd.String("output")
					return RunDownloadTUI(outputDir)
				},
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal().Err(err).Msg("Command failed")
	}
}
