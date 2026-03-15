package main

// TODO: implement Azure Functions adapter
// This is a thin wrapper that bridges Azure Functions HTTP trigger events
// to the webhook handlers in internal/infrastructure/controllers/webhooks/.
//
// Implementation steps:
// 1. Parse Azure Functions HTTP trigger input into http.Request
// 2. Route to the appropriate webhook handler (GitHub or Azure DevOps)
// 3. Capture the http.Response and return as Azure Functions output

func main() {
	panic("Azure Functions adapter not yet implemented")
}
