module github.com/splax/localvercel/cmd/peep

go 1.24.0

require (
	github.com/splax/localvercel/pkg v0.0.0
	golang.org/x/term v0.23.0
)

require golang.org/x/sys v0.23.0 // indirect

replace github.com/splax/localvercel/pkg => ../../pkg
