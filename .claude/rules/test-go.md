---
paths: 
  - **/*test.go
---
 
# Test Writing Standards
 
- Use descriptive test names: "should [action] when [condition]"
- One assertion per test when possible
- Mock external dependencies, never real APIs
- Include edge cases: empty inputs, null values, boundaries
- Table-driven tests for all business logic
- Mock external services with interfaces — no mock frameworks needed
- Test files alongside source: `email.go` → `email_test.go`
- If a test cannot be written for a section of code, say so at the code:
  put a comment starting `not covered:` on the block (or on the function,
  when the whole thing is uncoverable) giving the reason. There is no
  exclusion file; coverage numbers are reported unfiltered, so an
  uncovered block is either explained in place or a real gap.
- Reach for `not covered:` only when a test genuinely cannot exist:
  unreachable defensive branches, cgo entry points needing a live host
  facility, `crypto/rand` failures. "Awkward to reach" is a gap to write a
  test for, not to annotate. If a note says what it would take to cover
  the block, that is a worklist item, not an exemption.
