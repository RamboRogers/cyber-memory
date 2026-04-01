//go:build cgo && (ORT || ALL)

// Package embed wraps hugot's FeatureExtractionPipeline to produce
// 768-dim L2-normalised float32 embeddings using EmbeddingGemma-300m via ONNX Runtime.
package embed

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/backends"
	"github.com/knights-analytics/hugot/options"
	"github.com/knights-analytics/hugot/pipelines"

	"github.com/ramborogers/cyber-memory/assets"
)

const (
	// HFModelID is the HuggingFace model identifier for the ONNX build of EmbeddingGemma-300m.
	HFModelID = "onnx-community/embeddinggemma-300m-ONNX"

	// EmbedDim is the default output dimension.
	EmbedDim = 768
)

// Engine holds the hugot session and the feature extraction pipeline.
type Engine struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	log      *slog.Logger
}

// New creates an Engine. dataDir is the base directory used for both the
// extracted ORT library and the downloaded model (e.g. the same dir as the DB).
// ortLibPath overrides the auto-resolved library path (also: $CYBER_MEMORY_ORT).
func New(dataDir string, ortLibPath string, log *slog.Logger) (*Engine, error) {
	modelDir := filepath.Join(dataDir, "models")
	if err := os.MkdirAll(modelDir, 0o755); err != nil {
		return nil, fmt.Errorf("create model dir: %w", err)
	}

	// Extract the embedded ORT library if available and not already present.
	if ortLibPath == "" {
		extracted, err := ensureOrtLib(dataDir, log)
		if err != nil {
			return nil, err
		}
		if extracted != "" {
			ortLibPath = extracted
		} else {
			ortLibPath = defaultORTLibPath()
		}
	}

	sessionOpts := []options.WithOption{}
	if ortLibPath != "" {
		sessionOpts = append(sessionOpts, options.WithOnnxLibraryPath(ortLibPath))
	}

	session, err := hugot.NewORTSession(sessionOpts...)
	if err != nil {
		return nil, fmt.Errorf("create hugot ORT session: %w", err)
	}

	modelPath, err := ensureModel(modelDir, log)
	if err != nil {
		_ = session.Destroy()
		return nil, err
	}

	pipeline, err := hugot.NewPipeline(session, hugot.FeatureExtractionConfig{
		Name:         "cyber-memory-embed",
		ModelPath:    modelPath,
		OnnxFilename: "model_quantized.onnx",
		Options: []hugot.FeatureExtractionOption{
			pipelines.WithNormalization(), // L2 normalise after mean pool
		},
	})
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("create feature extraction pipeline: %w", err)
	}

	log.Info("embedding engine ready", "model", HFModelID, "dim", EmbedDim)
	return &Engine{session: session, pipeline: pipeline, log: log}, nil
}

// Embed returns a 768-dim L2-normalised embedding for the given text.
// For retrieval tasks the text is prefixed per EmbeddingGemma's recommended format.
func (e *Engine) Embed(text string) ([]float32, error) {
	out, err := e.pipeline.RunPipeline([]string{queryPrefix(text)})
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(out.Embeddings) == 0 {
		return nil, fmt.Errorf("embed: empty output")
	}
	return out.Embeddings[0], nil
}

// EmbedBatch embeds multiple texts in one ONNX call (max 32 recommended).
func (e *Engine) EmbedBatch(texts []string) ([][]float32, error) {
	prefixed := make([]string, len(texts))
	for i, t := range texts {
		prefixed[i] = queryPrefix(t)
	}
	out, err := e.pipeline.RunPipeline(prefixed)
	if err != nil {
		return nil, fmt.Errorf("embed batch: %w", err)
	}
	return out.Embeddings, nil
}

// EmbedDocument embeds text intended for storage (not a query).
// Uses the "search result" task prefix.
func (e *Engine) EmbedDocument(text string) ([]float32, error) {
	out, err := e.pipeline.RunPipeline([]string{documentPrefix(text)})
	if err != nil {
		return nil, fmt.Errorf("embed document: %w", err)
	}
	if len(out.Embeddings) == 0 {
		return nil, fmt.Errorf("embed document: empty output")
	}
	return out.Embeddings[0], nil
}

// Close destroys the hugot session and releases ONNX Runtime resources.
func (e *Engine) Close() error {
	return e.session.Destroy()
}

// ---- helpers ----

// queryPrefix prepends the EmbeddingGemma recommended task prefix for queries.
func queryPrefix(text string) string {
	return "task: search query | query: " + text
}

// documentPrefix prepends the task prefix for documents/passages being stored.
func documentPrefix(text string) string {
	return "task: search result | query: " + text
}

// onnxSubdir is where the model files live inside the hugot model directory.
const onnxSubdir = "onnx"

// ensureModel downloads the model from HuggingFace if not already present.
// The ONNX model uses external data (weights in a _data sidecar file), so
// hugot's built-in downloader is bypassed: we fetch both files via HTTP.
// Returns the local hugot model directory path.
func ensureModel(modelDir string, log *slog.Logger) (string, error) {
	expectedDir := filepath.Join(modelDir, "onnx-community_embeddinggemma-300m-ONNX")
	onnxDir := filepath.Join(expectedDir, onnxSubdir)
	dataFile := filepath.Join(onnxDir, "model_quantized.onnx_data")

	if _, err := os.Stat(dataFile); err == nil {
		log.Debug("model already present", "path", expectedDir)
		return expectedDir, nil
	}

	// First: let hugot download the tokenizer/config files.
	log.Info("downloading model tokenizer files…", "model", HFModelID)
	dlOpts := hugot.NewDownloadOptions()
	dlOpts.Verbose = false
	dlOpts.OnnxFilePath = "onnx/model_quantized.onnx"
	if _, err := hugot.DownloadModel(HFModelID, modelDir, dlOpts); err != nil {
		return "", fmt.Errorf("download model metadata: %w", err)
	}

	// Second: download ONNX graph + external weights directly (hugot only fetches one file).
	if err := os.MkdirAll(onnxDir, 0o755); err != nil {
		return "", fmt.Errorf("create onnx dir: %w", err)
	}
	base := "https://huggingface.co/onnx-community/embeddinggemma-300m-ONNX/resolve/main/onnx/"
	for _, fname := range []string{"model_quantized.onnx", "model_quantized.onnx_data"} {
		dest := filepath.Join(onnxDir, fname)
		if _, err := os.Stat(dest); err == nil {
			continue // already present
		}
		log.Info("downloading model file", "file", fname)
		if err := downloadFile(base+fname, dest); err != nil {
			return "", fmt.Errorf("download %s: %w", fname, err)
		}
	}
	log.Info("model ready", "path", expectedDir)
	return expectedDir, nil
}

// downloadFile fetches url and writes it to dest using a streaming copy.
func downloadFile(url, dest string) error {
	resp, err := http.Get(url) //nolint:gosec // URL is a hardcoded HuggingFace path
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// ensureOrtLib extracts the embedded ORT shared library to dataDir if it isn't
// already there. Returns the directory path for WithOnnxLibraryPath, or "" if
// no library is embedded for this platform.
func ensureOrtLib(dataDir string, log *slog.Logger) (string, error) {
	if len(assets.OrtLib) == 0 || assets.OrtLibName == "" {
		return "", nil // no embedded lib for this platform
	}
	dest := filepath.Join(dataDir, assets.OrtLibName)
	if _, err := os.Stat(dest); err == nil {
		return dataDir, nil // already extracted
	}
	log.Info("extracting embedded ORT library", "dest", dest)
	if err := os.WriteFile(dest, assets.OrtLib, 0o755); err != nil {
		return "", fmt.Errorf("extract ORT library: %w", err)
	}
	return dataDir, nil
}

// defaultORTLibPath returns the directory containing the ORT shared library.
// WithOnnxLibraryPath expects a directory; it appends the platform filename itself.
// Returns empty string to use onnxruntime_go's built-in default (linux/windows).
func defaultORTLibPath() string {
	switch runtime.GOOS {
	case "darwin":
		for _, candidate := range []string{
			"/usr/local/lib",
			"/opt/homebrew/lib",
			"/usr/lib",
		} {
			// hugot looks for libonnxruntime.dylib in the directory
			if _, err := os.Stat(candidate + "/libonnxruntime.dylib"); err == nil {
				return candidate
			}
		}
		return "/usr/local/lib" // best guess — clear error if missing
	case "linux":
		return "" // hugot defaults to /usr/lib on linux
	default:
		return ""
	}
}

// Ensure backends import is used (needed for pipeline option type).
var _ = backends.PipelineConfig[*pipelines.FeatureExtractionPipeline]{}
