package main

// TODO: implement AWS Lambda adapter
// This is a thin wrapper that bridges AWS API Gateway events
// to the webhook handlers in internal/infrastructure/controllers/webhooks/.
//
// Implementation steps:
// 1. Parse API Gateway proxy event into http.Request
// 2. Route to the appropriate webhook handler (GitHub or Azure DevOps)
// 3. Capture the http.Response and return as API Gateway proxy response

func main() {
	panic("Lambda adapter not yet implemented")
}
