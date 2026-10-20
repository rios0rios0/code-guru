//go:build !linux

package support

// HardenAgainstChildInspection is a no-op outside Linux, where the
// `/proc/<pid>` exposure it closes does not exist in the same form.
func HardenAgainstChildInspection() {}
