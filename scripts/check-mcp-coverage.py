"""Report statement coverage and gate the new MCP OAuth implementation."""

import json
import os
import pathlib
import sys

profile = pathlib.Path(sys.argv[1])
counts = {"overall": [0, 0], "mcp": [0, 0]}
for row in profile.read_text().splitlines()[1:]:
    position, statements, executions = row.split()
    total, covered = int(statements), int(statements) if int(executions) else 0
    for group in ["overall"] + (["mcp"] if "/cmd/auth/mcp_" in position else []):
        counts[group][0] += total
        counts[group][1] += covered

percentages = {key: covered / total * 100 if total else 0 for key, (total, covered) in counts.items()}
report = "\n".join([
    "### Auth OAuth coverage",
    "",
    "| Scope | Statement coverage |",
    "| --- | ---: |",
    f"| New MCP OAuth | {percentages['mcp']:.1f}% |",
    f"| Complete Go module | {percentages['overall']:.1f}% |",
    "",
    "Includes unit tests and an isolated real MySQL consent, PKCE, refresh, exchange, and revocation flow.",
    "The 80% gate applies to new MCP OAuth code; complete legacy coverage is reported separately.",
]) + "\n"
print(report)
pathlib.Path("mcp-coverage.md").write_text(report)
pathlib.Path("mcp-coverage.json").write_text(json.dumps(percentages))
if os.environ.get("GITHUB_STEP_SUMMARY"):
    with open(os.environ["GITHUB_STEP_SUMMARY"], "a") as output:
        output.write(report)
if not counts["mcp"][0] or percentages["mcp"] < 80:
    raise SystemExit("New MCP OAuth statement coverage must reach 80%")
