# Combined fork-side evaluation branch

This branch merges two independent changes based on the same immutable `dev-public` SHA:

- #44 full GET+SHA-256 WebDAV upload verification (tested-provider contract only);
- #46 propagating local-folder listing errors (partial fix).

It also brings forward the original baseline fault-injection witnesses for #44, #45 and #46 and the research documentation. The #45 direct-final-name PUT issue remains **unfixed**; do not describe this integration branch as a complete cloud publication safety fix.

**The script creates and pushes *only* fork research branches. It does not modify or open PRs against `Liquid-co/OpenSave`, and it does not merge into the fork's main branch.**
