# Security policy

## Supported versions

| Version | Supported |
| --- | --- |
| `dev` | Yes |
| 0.x releases | Best effort |

## Reporting a vulnerability

Report security issues privately to the repository owner instead of opening a
public issue. Include reproduction steps, affected versions, and expected
impact when possible.

## Log-content boundary

Logs may contain HTML, JavaScript, ANSI escape sequences, control characters,
credentials, tokens, and personal data. LogLens treats every source as
untrusted:

- the original filename is metadata only;
- managed filenames use generated UUIDs;
- content is rendered only as inert text;
- ANSI and unsafe control characters are removed from API display responses;
- raw sources remain immutable;
- secret masking must be explicit and reversible at display/export time.

Server-path imports must remain below the configured import root, reject
symlinks and traversal, and never accept arbitrary absolute paths.

