---
"@notploy/sdk": patch
---

Expose the client runtime from the package entry point. The generated code now
lives in `src/generated/` behind a hand-written `src/index.ts`, so
`import { client, createClient } from "@notploy/sdk"` works as documented and
multi-instance consumers can build one client per Notploy instance.
