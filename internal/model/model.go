package model

import "time"

// Contact represents a phonebook entry produced from YAML files.
type Contact struct {
	FirstName    string
	LastName     string
	Phone        string
	AccountIndex int
	GroupID      *int
	Nickname     string
	SourcePath   string
	SourceMod    time.Time
}

// Clone returns a deep copy to avoid shared pointers.
func (c Contact) Clone() Contact {
	clone := c
	if c.GroupID != nil {
		val := *c.GroupID
		clone.GroupID = &val
	}
	return clone
}
