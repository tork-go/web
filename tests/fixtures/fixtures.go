// Package fixtures holds types the tests need to live in a package other than
// their own — which today means one thing: proving that two types sharing a
// name still get one component each.
package fixtures

// Item shares its name with a type in the test package, so a document that
// describes both has to tell them apart.
type Item struct {
	Label string `json:"label"`
}
