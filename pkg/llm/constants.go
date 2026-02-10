package llm

// StopReason constants define normalized reasons for LLM generation termination.
// All providers must normalize their native stop reasons to these values.
const (
	StopReasonStop   = "stop"   // Normal completion
	StopReasonLength = "length" // Output truncated due to token limit
)

// ContentBlock Type constants define the supported content block formats
// used throughout the message pipeline.
const (
	BlockTypeText     = "text"     // Plain text content
	BlockTypeThinking = "thinking" // Internal reasoning/chain-of-thought
	BlockTypeImage    = "image"    // Binary image data
	BlockTypeError    = "error"    // Error message displayed to user
)
