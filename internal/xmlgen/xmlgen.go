package xmlgen

import (
	"encoding/xml"
	"strings"

	"github.com/n3wscott/phonebook/internal/model"
)

// Build generates Grandstream-compatible XML from contacts.
func Build(contacts []model.Contact) ([]byte, error) {
	book := xmlPhonebook{Contacts: make([]xmlContact, 0, len(contacts))}
	for _, c := range contacts {
		xc := xmlContact{
			LastName:  strings.TrimSpace(c.LastName),
			FirstName: strings.TrimSpace(c.FirstName),
			Phone: xmlPhone{
				Number:       strings.TrimSpace(c.Phone),
				AccountIndex: c.AccountIndex,
			},
		}
		if c.GroupID != nil {
			xc.Groups = &xmlGroups{GroupID: *c.GroupID}
		}
		book.Contacts = append(book.Contacts, xc)
	}

	payload, err := xml.MarshalIndent(book, "", "  ")
	if err != nil {
		return nil, err
	}
	final := append([]byte(xml.Header), payload...)
	if len(final) == 0 || final[len(final)-1] != '\n' {
		final = append(final, '\n')
	}
	return final, nil
}

type xmlPhonebook struct {
	XMLName  xml.Name     `xml:"AddressBook"`
	Contacts []xmlContact `xml:"Contact"`
}

type xmlContact struct {
	LastName  string     `xml:"LastName,omitempty"`
	FirstName string     `xml:"FirstName,omitempty"`
	Phone     xmlPhone   `xml:"Phone"`
	Groups    *xmlGroups `xml:"Groups,omitempty"`
}

type xmlPhone struct {
	Number       string `xml:"phonenumber"`
	AccountIndex int    `xml:"accountindex"`
}

type xmlGroups struct {
	GroupID int `xml:"groupid"`
}
