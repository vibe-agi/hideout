# Workload observation fixtures

These fixtures are deterministic and local-only. They define the same facts for
unit, refinement, TUI/API parity, and real Lima gates:

- `fixture.json` defines the DNS, HTTP, SOCKS, PID reuse, and event-loss facts;
- `reference-workload.sh` produces bounded process and filesystem activity and
  uses only fixture endpoints passed by the test;
- `credential-canaries.json` enumerates synthetic credential shapes that must
  be absent after the pre-persistence redaction boundary;
- `expected-activity.json` names the minimum observable facts. A missing fact
  is a test failure unless the corresponding coverage interval is explicitly
  reduced before the query result is rendered.

The fixture does not contact the public internet and contains no real
credential. Tests must bind all listeners to loopback or the isolated guest
test network.
