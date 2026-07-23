// Package openapi is the OpenAPI document as Go types, plus a way to write
// them out.
//
// It knows nothing about routes, handlers, dependencies, or the framework that
// builds documents with it. That boundary is deliberate: the builder that
// reads an application and the emitter that writes a specification change for
// different reasons, and a 3.2 emitter should be able to join by writing
// another walk of the same model rather than by touching the model itself.
//
// Both emitters render one ordered tree rather than traversing the model
// twice, which is what makes JSON and YAML two views of a single document
// instead of two descriptions that can drift. It is also how determinism is
// settled: the specification's objects are unordered, but a document whose
// shape changes between builds of the same application cannot be reviewed as a
// diff, so order is fixed by whoever fills the model in and preserved from
// there — nothing here ranges over a map on the way to output.
//
// The YAML emitter is hand-rolled. A specification document needs a narrow
// subset of YAML — mappings, sequences, strings, numbers, booleans — and
// writing that is smaller than the code it would take to configure a general
// library, which keeps this module's promise of importing nothing outside the
// standard library.
package openapi
