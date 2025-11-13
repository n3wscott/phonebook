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
		phones := collectPhones(c)
		xc := xmlContact{
			LastName:  strings.TrimSpace(c.LastName),
			FirstName: strings.TrimSpace(c.FirstName),
			Phones:    phones,
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

func collectPhones(c model.Contact) []xmlPhone {
	if len(c.Phones) == 0 {
		idx := 1
		if c.AccountIndex != nil {
			idx = *c.AccountIndex
		}
		return []xmlPhone{{
			Number:       strings.TrimSpace(c.Extension),
			AccountIndex: idx,
		}}
	}
	out := make([]xmlPhone, 0, len(c.Phones))
	for _, p := range c.Phones {
		out = append(out, xmlPhone{
			Number:       strings.TrimSpace(p.Number),
			AccountIndex: p.AccountIndex,
		})
	}
	return out
}

type xmlPhonebook struct {
	XMLName  xml.Name     `xml:"AddressBook"`
	Contacts []xmlContact `xml:"Contact"`
}

type xmlContact struct {
	LastName  string     `xml:"LastName,omitempty"`
	FirstName string     `xml:"FirstName,omitempty"`
	Phones    []xmlPhone `xml:"Phone"`
	Groups    *xmlGroups `xml:"Groups,omitempty"`
}

type xmlPhone struct {
	Number       string `xml:"phonenumber"`
	AccountIndex int    `xml:"accountindex"`
}

type xmlGroups struct {
	GroupID int `xml:"groupid"`
}
