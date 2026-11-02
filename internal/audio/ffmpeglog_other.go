//go:build !(linux && (amd64 || arm64))

package audio

// silenceFFmpegLogs is a no-op outside Linux amd64/arm64: GUI bundles on
// Windows/macOS have no attached console, so FFmpeg's default stderr logging
// never reaches the user.
func silenceFFmpegLogs() {}
