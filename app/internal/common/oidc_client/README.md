Taken from: https://github.com/coryschwartz/gin-oidc-client
Copyright 2025 Cory Schwartz

Issues with original:
- Changed session options so that it ends up making a MaxAge setting by overriding defaults making the return from auth generate a new session id
- No way to trap MiddlewareRequireLogin so that you can optionally not redirect 