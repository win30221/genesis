package tools

// ActionRequest represents a standardized payload for controlling a plugin or worker.
// It follows the "Action Dispatching" pattern to decouple the tool definition
// from the platform-specific execution details.
type ActionRequest struct {
	Action string         `json:"action"` // Name of the capability to invoke (e.g., "screenshot")
	Params map[string]any `json:"params"` // Key-value map of parameters required by the action
}

// ActionResponse encapsulates the result of an action execution from a Controller.
type ActionResponse struct {
	Success bool   `json:"success"`         // Indicates if the action completed without fatal errors
	Data    any    `json:"data,omitempty"`  // The primary result payload (e.g., image bytes, command output)
	Error   string `json:"error,omitempty"` // User-friendly error message if Success is false
}

// Controller is the universal interface for plugin control units (Workers).
// It abstracts the underlying platform complexity by providing a
// dispatch-based execution model.
type Controller interface {
	// Execute dispatches and performs a specified action based on the request.
	Execute(req ActionRequest) (*ActionResponse, error)

	// Capabilities returns a listing of all primitive actions (verbs)
	// supported by this specific controller instance.
	Capabilities() []string
}
