package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ModelInfo describes a downloadable model
type ModelInfo struct {
	Name     string
	Filename string
	URL      string
	Size     int64  // bytes
	Desc     string // Description of the model
}

// Implement list.Item interface
func (m ModelInfo) Title() string       { return m.Name }
func (m ModelInfo) Description() string { return fmt.Sprintf("%s (%s)", m.Desc, formatBytes(m.Size)) }
func (m ModelInfo) FilterValue() string { return m.Name }

// Available models - top tier only
// URLs follow whisper.cpp official download scripts:
// - Whisper models: https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-{model}.bin
// - VAD models: https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-{model}.bin
// - Parakeet models: https://huggingface.co/ggml-org/parakeet-GGUF/resolve/main/ggml-parakeet-{model}.bin
var availableModels = []ModelInfo{
	{
		Name:     "Large V3 Turbo",
		Filename: "ggml-large-v3-turbo.bin",
		URL:      "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3-turbo.bin",
		Size:     1_624_199_872,
		Desc:     "Fastest large model, excellent quality",
	},
	{
		Name:     "Large V3",
		Filename: "ggml-large-v3.bin",
		URL:      "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-large-v3.bin",
		Size:     3_095_033_483,
		Desc:     "Best quality, slower",
	},
	{
		Name:     "Medium",
		Filename: "ggml-medium.bin",
		URL:      "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-medium.bin",
		Size:     1_533_774_781,
		Desc:     "Good balance of speed and quality",
	},
	{
		Name:     "Parakeet TDT 0.6B v3 (F16)",
		Filename: "ggml-parakeet-tdt-0.6b-v3-f16.bin",
		URL:      "https://huggingface.co/ggml-org/parakeet-GGUF/resolve/main/ggml-parakeet-tdt-0.6b-v3-f16.bin",
		Size:     1_255_897_319,
		Desc:     "Fast multilingual ASR, no silence hallucination",
	},
	{
		// Matches F16 exactly on short clips, but terminates early on longer
		// audio - it dropped the closing clause of a 2.5 min recording, where
		// F16 got it. Q8_0 quantizes the TDT decoder LSTM and joint net (block
		// size 32 divides n_pred_dim=640), which is the likely cause.
		Name:     "Parakeet TDT 0.6B v3 (Q8_0)",
		Filename: "ggml-parakeet-tdt-0.6b-v3-q8_0.bin",
		URL:      "https://huggingface.co/ggml-org/parakeet-GGUF/resolve/main/ggml-parakeet-tdt-0.6b-v3-q8_0.bin",
		Size:     668_757_119,
		Desc:     "Smaller Parakeet, may truncate long recordings",
	},
	{
		Name:     "Silero VAD v6.2.0",
		Filename: "ggml-silero-v6.2.0.bin",
		URL:      "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v6.2.0.bin",
		Size:     885_098,
		Desc:     "Voice Activity Detection model",
	},
}

// Download state machine
type downloadState int

const (
	stateSelectingModel downloadState = iota
	stateDownloading
	stateDone
	stateError
)

// Messages
type downloadProgressMsg struct {
	downloaded int64
	total      int64
}

type downloadCompleteMsg struct {
	filename string
}

type downloadErrorMsg struct {
	err error
}

type tickMsg time.Time

// Model for the download TUI
type downloadModel struct {
	state      downloadState
	list       list.Model
	progress   progress.Model
	selected   ModelInfo
	downloaded int64
	total      int64
	err        error
	outputDir  string
	quitting   bool
	progressCh chan downloadProgressMsg
}

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FAFAFA")).
			Background(lipgloss.Color("#7D56F4")).
			Padding(0, 1)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF0000")).
			Bold(true)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#04B575")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))
)

func newDownloadModel(outputDir string) downloadModel {
	// Create list items
	items := make([]list.Item, len(availableModels))
	for i, m := range availableModels {
		items[i] = m
	}

	// Configure list
	delegate := list.NewDefaultDelegate()
	l := list.New(items, delegate, 60, 14)
	l.Title = "Select a model to download"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle

	// Configure progress bar
	p := progress.New(
		progress.WithDefaultGradient(),
		progress.WithWidth(50),
	)

	return downloadModel{
		state:      stateSelectingModel,
		list:       l,
		progress:   p,
		outputDir:  outputDir,
		progressCh: make(chan downloadProgressMsg, 100),
	}
}

func (m downloadModel) Init() tea.Cmd {
	return nil
}

func (m downloadModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		case "enter":
			if m.state == stateSelectingModel {
				selected, ok := m.list.SelectedItem().(ModelInfo)
				if ok {
					m.selected = selected
					m.state = stateDownloading
					m.total = selected.Size
					// Start download and tick for progress updates
					return m, tea.Batch(
						m.startDownload(),
						tickCmd(),
					)
				}
			}
			if m.state == stateDone || m.state == stateError {
				return m, tea.Quit
			}
		}

	case tickMsg:
		if m.state != stateDownloading {
			return m, nil
		}
		// Check for progress updates
		select {
		case prog := <-m.progressCh:
			m.downloaded = prog.downloaded
			m.total = prog.total
			if m.total > 0 {
				percent := float64(m.downloaded) / float64(m.total)
				cmd := m.progress.SetPercent(percent)
				return m, tea.Batch(cmd, tickCmd())
			}
		default:
		}
		return m, tickCmd()

	case downloadCompleteMsg:
		m.state = stateDone
		// Set progress to 100%
		cmd := m.progress.SetPercent(1.0)
		return m, cmd

	case downloadErrorMsg:
		m.state = stateError
		m.err = msg.err
		return m, nil

	case progress.FrameMsg:
		progressModel, cmd := m.progress.Update(msg)
		m.progress = progressModel.(progress.Model)
		return m, cmd

	case tea.WindowSizeMsg:
		m.list.SetWidth(msg.Width)
		return m, nil
	}

	// Handle list updates when in selection state
	if m.state == stateSelectingModel {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	return m, nil
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m downloadModel) View() string {
	if m.quitting {
		return ""
	}

	var s strings.Builder

	switch m.state {
	case stateSelectingModel:
		s.WriteString(m.list.View())
		s.WriteString("\n")
		s.WriteString(helpStyle.Render("↑/↓: navigate • enter: select • q: quit"))

	case stateDownloading:
		s.WriteString(titleStyle.Render("Downloading"))
		s.WriteString("\n\n")
		s.WriteString(statusStyle.Render(fmt.Sprintf("Model: %s", m.selected.Name)))
		s.WriteString("\n")
		s.WriteString(statusStyle.Render(fmt.Sprintf("File: %s", m.selected.Filename)))
		s.WriteString("\n\n")
		s.WriteString(m.progress.View())
		s.WriteString("\n\n")
		s.WriteString(statusStyle.Render(fmt.Sprintf("%s / %s",
			formatBytes(m.downloaded),
			formatBytes(m.total))))
		s.WriteString("\n\n")
		s.WriteString(helpStyle.Render("ctrl+c: cancel"))

	case stateDone:
		s.WriteString(successStyle.Render("✓ Download complete!"))
		s.WriteString("\n\n")
		s.WriteString(statusStyle.Render(fmt.Sprintf("Saved to: %s/%s", m.outputDir, m.selected.Filename)))
		s.WriteString("\n\n")
		s.WriteString(helpStyle.Render("press enter or q to exit"))

	case stateError:
		s.WriteString(errorStyle.Render("✗ Download failed!"))
		s.WriteString("\n\n")
		s.WriteString(statusStyle.Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
		s.WriteString(helpStyle.Render("press enter or q to exit"))
	}

	return s.String()
}

func (m downloadModel) startDownload() tea.Cmd {
	return func() tea.Msg {
		// Ensure output directory exists
		if err := os.MkdirAll(m.outputDir, 0755); err != nil {
			return downloadErrorMsg{err: fmt.Errorf("failed to create output directory: %w", err)}
		}

		outputPath := filepath.Join(m.outputDir, m.selected.Filename)

		// Create output file
		out, err := os.Create(outputPath)
		if err != nil {
			return downloadErrorMsg{err: fmt.Errorf("failed to create file: %w", err)}
		}
		defer func() { _ = out.Close() }()

		// Start HTTP request
		resp, err := http.Get(m.selected.URL)
		if err != nil {
			return downloadErrorMsg{err: fmt.Errorf("failed to download: %w", err)}
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return downloadErrorMsg{err: fmt.Errorf("bad status: %s", resp.Status)}
		}

		// Get total size from Content-Length if available
		total := resp.ContentLength
		if total <= 0 {
			total = m.selected.Size
		}

		// Copy with progress tracking
		var downloaded int64
		buf := make([]byte, 32*1024) // 32KB buffer

		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				_, writeErr := out.Write(buf[:n])
				if writeErr != nil {
					return downloadErrorMsg{err: fmt.Errorf("failed to write: %w", writeErr)}
				}
				downloaded += int64(n)

				// Send progress update (non-blocking)
				select {
				case m.progressCh <- downloadProgressMsg{downloaded: downloaded, total: total}:
				default:
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				return downloadErrorMsg{err: fmt.Errorf("failed to read: %w", err)}
			}
		}

		return downloadCompleteMsg{filename: m.selected.Filename}
	}
}

// formatBytes formats bytes as human-readable string
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// RunDownloadTUI starts the interactive download TUI
func RunDownloadTUI(outputDir string) error {
	p := tea.NewProgram(newDownloadModel(outputDir))
	_, err := p.Run()
	return err
}
