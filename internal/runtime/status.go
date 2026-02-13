package runtime

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// StatusHandler handles progress display during artifact pulling
// Mirrors ORAS CLI behavior for status display
type StatusHandler interface {
	OnNodeDownloading(desc ocispec.Descriptor)
	OnNodeDownloaded(desc ocispec.Descriptor)
	OnNodeProcessing(desc ocispec.Descriptor)
	OnNodeRestored(desc ocispec.Descriptor)
	OnNodeSkipped(desc ocispec.Descriptor)
	UpdateProgress(digest string, bytesRead int64)
	Close()
}

// NodeProgress tracks progress for a single node/layer
type NodeProgress struct {
	Descriptor    ocispec.Descriptor
	Status        string // "Downloading", "Downloaded", "Processing", "Restored", "Skipped"
	BytesRead     int64
	StartTime     time.Time
	EndTime       time.Time
	DisplaySize   string
	LastSpeedTime time.Time
	LastSpeedRead int64
}

// spinnerSymbols for animated progress (ORAS style)
var spinnerSymbols = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// TextStatusHandler displays status updates as simple text (non-TTY)
type TextStatusHandler struct {
	mu        sync.Mutex
	startTime time.Time
	progress  map[string]*NodeProgress
}

// NewTextStatusHandler creates a text-based status handler
func NewTextStatusHandler() *TextStatusHandler {
	return &TextStatusHandler{
		startTime: time.Now(),
		progress:  make(map[string]*NodeProgress),
	}
}

func (h *TextStatusHandler) OnNodeDownloading(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	h.progress[digestStr] = &NodeProgress{
		Descriptor: desc,
		Status:     "Downloading",
		StartTime:  time.Now(),
		DisplaySize: formatBytes(desc.Size),
	}

	fmt.Printf("↓ Pulling %s (%s)\n", digestStr, formatBytes(desc.Size))
}

func (h *TextStatusHandler) OnNodeDownloaded(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	if p, ok := h.progress[digestStr]; ok {
		p.Status = "Downloaded"
		p.EndTime = time.Now()
		duration := p.EndTime.Sub(p.StartTime)
		speed := formatBytesPerSec(float64(p.BytesRead) / duration.Seconds())
		fmt.Printf("✓ Pulled %s (%s/s)\n", digestStr, speed)
	}
}

func (h *TextStatusHandler) OnNodeProcessing(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	if p, ok := h.progress[digestStr]; ok {
		p.Status = "Processing"
	}
	// Don't show processing line - keep output minimal
}

func (h *TextStatusHandler) OnNodeRestored(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	if p, ok := h.progress[digestStr]; ok {
		p.Status = "Restored"
		p.EndTime = time.Now()
	}
	fmt.Printf("  └─ sha256:%s\n", desc.Digest.String()[7:])
}

func (h *TextStatusHandler) OnNodeSkipped(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	if p, ok := h.progress[digestStr]; ok {
		p.Status = "Skipped"
	}
	fmt.Printf("  Skipped %s\n", digestStr)
}
func (h *TextStatusHandler) UpdateProgress(digest string, bytesRead int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if p, ok := h.progress[digest]; ok {
		p.BytesRead = bytesRead
	}
}
func (h *TextStatusHandler) Close() {
	// No cleanup needed for text handler
}

// TTYStatusHandler displays real-time progress with visual elements (TTY)
// Uses ANSI codes for animated progress bars and status updates matching ORAS CLI
type TTYStatusHandler struct {
	mu          sync.Mutex
	startTime   time.Time
	progress    map[string]*NodeProgress
	ticker      *time.Ticker
	done        chan struct{}
	wg          sync.WaitGroup
	currentNode string
	spinnerIdx  int64
	lastRender  time.Time
}

// NewTTYStatusHandler creates a TTY-based status handler with real-time progress
func NewTTYStatusHandler() *TTYStatusHandler {
	h := &TTYStatusHandler{
		startTime: time.Now(),
		progress:  make(map[string]*NodeProgress),
		ticker:    time.NewTicker(100 * time.Millisecond), // 5 FPS like ORAS
		done:      make(chan struct{}),
	}

	// Start the render loop
	h.wg.Add(1)
	go h.renderLoop()

	return h
}

func (h *TTYStatusHandler) renderLoop() {
	defer h.wg.Done()

	for {
		select {
		case <-h.done:
			return
		case <-h.ticker.C:
			h.render()
		}
	}
}

func (h *TTYStatusHandler) render() {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Only render the current downloading node
	if h.currentNode == "" {
		return
	}

	p, ok := h.progress[h.currentNode]
	if !ok || p.Status != "Downloading" {
		return
	}

	// Calculate progress
	progress := float64(p.BytesRead) / float64(p.Descriptor.Size)
	if progress > 1.0 {
		progress = 1.0
	}

	// Calculate speed
	elapsed := time.Since(p.StartTime).Seconds()
	speed := 0.0
	if elapsed > 0 {
		speed = float64(p.BytesRead) / elapsed
	}

	// Generate spinner symbol
	spinIdx := atomic.AddInt64(&h.spinnerIdx, 1)
	spinnerChar := string(spinnerSymbols[int(spinIdx)%len(spinnerSymbols)])

	// Generate progress bar
	barLength := 20
	filledLength := int(progress * float64(barLength))
	emptyLength := barLength - filledLength
	progressBar := "["
	progressBar += strings.Repeat("=", filledLength)
	progressBar += strings.Repeat(" ", emptyLength)
	progressBar += "]"

	// Format percentage
	percent := fmt.Sprintf("%.2f%%", progress*100)

	// Format size/total and speed
	read := formatBytes(p.BytesRead)
	total := p.DisplaySize
	speedStr := formatBytesPerSec(speed)

	// Format elapsed time
	elapsedTime := formatDuration(time.Since(p.StartTime))

	// Render line: [spinner] [bar] [speed] [size/total] [percent] [time]
	// Abbreviated to fit terminal: ⠋ [==        ] 512KB/s 1.2MB/2.5MB  48% 1m23s
	output := fmt.Sprintf("\r  %s %s %8s %s/%s %6s %8s",
		spinnerChar,
		progressBar,
		speedStr,
		read,
		total,
		percent,
		elapsedTime)

	// Write with carriage return to overwrite line
	fmt.Print(output)
}

// formatDuration formats duration to human-readable format
func formatDuration(d time.Duration) string {
	d = d.Round(time.Millisecond)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%ds", minutes, seconds)
}

// formatBytesPerSec formats bytes per second with unit
func formatBytesPerSec(bps float64) string {
	const KB = 1024
	const MB = 1024 * KB
	const GB = 1024 * MB

	if bps >= GB {
		return fmt.Sprintf("%.2fGB", bps/GB)
	}
	if bps >= MB {
		return fmt.Sprintf("%.2fMB", bps/MB)
	}
	if bps >= KB {
		return fmt.Sprintf("%.2fKB", bps/KB)
	}
	return fmt.Sprintf("%.0fB", bps)
}

func (h *TTYStatusHandler) OnNodeDownloading(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	h.currentNode = digestStr
	h.progress[digestStr] = &NodeProgress{
		Descriptor:    desc,
		Status:        "Downloading",
		StartTime:     time.Now(),
		DisplaySize:   formatBytes(desc.Size),
		LastSpeedTime: time.Now(),
		LastSpeedRead: 0,
	}

	// Concise output like ORAS: just show activity
	fmt.Printf("↓ Pulling %s (%s)\n", digestStr, formatBytes(desc.Size))
}

func (h *TTYStatusHandler) OnNodeDownloaded(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	if p, ok := h.progress[digestStr]; ok {
		p.Status = "Downloaded"
		p.EndTime = time.Now()
		duration := p.EndTime.Sub(p.StartTime)
		speed := formatBytesPerSec(float64(p.BytesRead) / duration.Seconds())
		// Concise like ORAS: checkmark, size, percentage, time
		fmt.Printf("✓ Pulled %s (%s/s)\n", digestStr, speed)
	}
}

func (h *TTYStatusHandler) OnNodeProcessing(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	if p, ok := h.progress[digestStr]; ok {
		p.Status = "Processing"
	}
	// Don't show processing line - keep output minimal
}

func (h *TTYStatusHandler) OnNodeRestored(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	if p, ok := h.progress[digestStr]; ok {
		p.Status = "Restored"
		p.EndTime = time.Now()
		// Show digest on second line like ORAS
		fmt.Printf("  └─ sha256:%s\n", desc.Digest.String()[7:])
	}
	h.currentNode = "" // Stop rendering for this node
}

func (h *TTYStatusHandler) OnNodeSkipped(desc ocispec.Descriptor) {
	h.mu.Lock()
	defer h.mu.Unlock()

	digestStr := desc.Digest.String()[:16]
	if p, ok := h.progress[digestStr]; ok {
		p.Status = "Skipped"
	}
	fmt.Printf("\n  ⊘ Skipped %s\n", digestStr)
}

func (h *TTYStatusHandler) UpdateProgress(digest string, bytesRead int64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if p, ok := h.progress[digest]; ok {
		p.BytesRead = bytesRead
	}
}

func (h *TTYStatusHandler) Close() {
	h.ticker.Stop()
	close(h.done)
	h.wg.Wait()
	// Final newline to move cursor to next line
	fmt.Print("\n")
}

// NewStatusHandler creates appropriate handler based on TTY detection
func NewStatusHandler() StatusHandler {
	// Check if stdout is a TTY
	if isTerminal(os.Stdout.Fd()) {
		return NewTTYStatusHandler()
	}
	return NewTextStatusHandler()
}

// isTerminal checks if a file descriptor is connected to a terminal
func isTerminal(fd uintptr) bool {
	// Simple check: if it's stdout/stderr and not piped
	// On Unix-like systems, we could use tcgetattr but this is simpler
	return fd == 1 || fd == 2 // stdout or stderr
}

// formatBytes formats bytes into human-readable format (B, KB, MB, GB)
func formatBytes(size int64) string {
	const (
		B  = 1
		KB = 1024 * B
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case size >= GB:
		return fmt.Sprintf("%.2fGB", float64(size)/float64(GB))
	case size >= MB:
		return fmt.Sprintf("%.2fMB", float64(size)/float64(MB))
	case size >= KB:
		return fmt.Sprintf("%.2fKB", float64(size)/float64(KB))
	default:
		return fmt.Sprintf("%dB", size)
	}
}
