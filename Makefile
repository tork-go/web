.PHONY: test test-cover cover-report

# The packages actually shipped as part of Tork Web. Coverage is scoped to
# these: tests live under tests/, outside the packages they exercise, so
# -coverpkg is required. Without it, go test has no local package to
# instrument and reports "no statements" instead of real coverage.
PRODUCT_PKGS := github.com/tork-go/web,github.com/tork-go/web/openapi

test:
	go test ./tests/...

# Unit test coverage for the shipped packages.
test-cover:
	go test -coverpkg=$(PRODUCT_PKGS) -coverprofile=coverage.out ./tests/...
	go tool cover -func=coverage.out

# The lines no test reached, which is the only useful way to read a coverage
# run that is meant to stay at 100%.
cover-report: test-cover
	go tool cover -func=coverage.out | grep -v '100.0%' || echo "every statement is covered"
