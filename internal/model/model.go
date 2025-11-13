package model

import "time"

// Phone represents a dialable number for XML output.
type Phone struct {
	Number       string
	AccountIndex int
}

// ContactAuth captures SIP auth credentials.
type ContactAuth struct {
	Username string
	Password string
}

// ContactAOR defines address-of-record options.
type ContactAOR struct {
	MaxContacts      int
	RemoveExisting   bool
	QualifyFrequency int
}

// ContactEndpoint configures template selection.
type ContactEndpoint struct {
	Template string
}

// Contact is the normalized representation of a user/extension.
type Contact struct {
	ID           string
	FirstName    string
	LastName     string
	Extension    string
	Password     string
	GroupID      *int
	AccountIndex *int
	Phones       []Phone
	Nickname     string

	Auth     ContactAuth
	AOR      ContactAOR
	Endpoint ContactEndpoint

	SourcePath string
	SourceMod  time.Time
}
