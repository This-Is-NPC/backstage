package recorder

// Recorder captures the screen to a file between Start and Stop.
type Recorder interface {
	// Start begins recording to outPath and returns once the encoder is warm.
	Start(outPath string) error
	// Stop finalizes the recording and returns the written file path.
	Stop() (path string, err error)
}
