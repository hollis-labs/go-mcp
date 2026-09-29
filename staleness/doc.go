// Package staleness compares verified image identities without managing any
// process lifecycle. Products supply image verification; launchers supply the
// actual replacement selector. Missing evidence always produces Unknown.
package staleness
