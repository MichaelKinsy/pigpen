# Credits

Autolith by Lambda Symbolics, autolith.rocks; no endorsement implied.

[Autolith](https://autolith.rocks) is a self-modifying Common Lisp coding agent by Lambda Symbolics OÜ
(<https://github.com/lambda-symbolics/autolith>, ISC licence, copyright (c) 2026 Lambda Symbolics OÜ; reviewed at commit
`505e814610cd4ad10ea8d30654fe89b4f1113341`). This Piglet is independent of Lambda Symbolics and of Autolith's authors: it is
not endorsed by them and implies no partnership.

No Autolith code, text or art is copied into this repository or into a Piglet Binary. The `autolith-bridge` extension is
original work by Michael Kinsy (MIT, see the repository [LICENSE](../../LICENSE)). It only runs the `autolith` program
(`autolith run-job`, `autolith --detached` and `autolith localgroup`) that the user installs separately, and reads the
S-expression events and files that program writes.

The extension builds on PiG's Common Lisp SDK (`extensions/sdk-lisp`, PiG v0.7.0), which PiG stages into the image it builds.
