---
paths: 
  - **/*test.ts
---
 
# Test Writing Standards
 
- Use descriptive test names: "should [action] when [condition]"
- One assertion per test when possible
- Mock external dependencies, never real APIs
- Include edge cases: empty inputs, null values, boundaries
- Table-driven tests for all business logic
- Mock external services no mock frameworks needed
