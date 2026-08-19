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
- If a test cannot be written for a section of code, document why in a
  comment at the function and add the exact uncovered line ranges to
  `exclude-from-coverage.txt` in the project root.
