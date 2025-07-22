package library

const (
	METHOD_POST    = 1
	METHOD_PUT     = 2
	METHOD_PATCH   = 3
	METHOD_DELETE  = 4
	METHOD_DEFAULT = 0

	ACTION_POST   = "created"
	ACTION_PUT    = "updated"
	ACTION_DELETE = "deleted"
	ACTION_PATCH  = "modified"
	ACTION_UNKNOWN = "accessed"
)
