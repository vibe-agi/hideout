<!-- markdownlint-disable MD013 -->

# Contract: Lima Real Run Reference Smoke

This contract defines the product-facing validation surface for the
`001-lima-real-run` feature. It is a CLI/gate contract, not a new public API.

## Command Shape

The implementation should provide a dedicated smoke entrypoint, for example:

```bash
scripts/test-lima-real-run.sh
```

The entrypoint may be called directly or through `scripts/test-phase1.sh` once
the feature is promoted.

## Inputs

| Input | Required | Meaning |
| --- | --- | --- |
| `HIDEOUT_STORE_ROOT` | no | Override store root for isolated test runs. |
| `LIMA_HOME` | no | Override Lima home for isolated test runs. |
| `HIDEOUT_LIMA_REAL_RUN_NETWORK` | no | `direct` by default; privacy mode requires proxy prerequisites. |
| `HIDEOUT_SECRET_DEFAULT_PROXY` | privacy mode only | Operator proxy secret ref source; value must not be logged. |
| `HIDEOUT_LIMA_REAL_RUN_TIMEOUT` | no | Maximum time for the reference run. |
| `HIDEOUT_LIMA_REAL_RUN_KEEP_TMP` | no | Keep temporary workspace/evidence for debugging. |

Implementation may add namespaced inputs for workspace, endpoint, or target CLI
selection, but product-specific package names, API keys, prompts, or accounts
must not become default gate inputs.

## Reference Workload Contract

The smoke must:

1. create or select a sanitized temporary workspace;
2. create a task file and expected output location;
3. run the configured generic target CLI with `hideout run --backend lima`;
4. make the target update the expected workspace file;
5. verify the result through a guest/workspace success check; host-side
   verification is limited to test-harness read-only assertions over produced
   workspace artifacts and must not execute an operator-provided host command;
6. make the target request an operator/test-declared endpoint through the
   selected network policy;
7. run a fixed boundary-triggering action set; and
8. verify evidence after the run.

## Required Output

The smoke should print stable non-secret markers:

```text
lima-real-run: workspace-updated=yes
lima-real-run: success-check=passed
lima-real-run: network=<direct|privacy>
lima-real-run: endpoint=reachable
lima-real-run: session=ses_...
lima-real-run: environment=env_...
lima-real-run: audit=<path>
lima-real-run: boundary=present
lima-real-run: evidence=passed
lima-real-run: passed
```

The exact wording may vary, but tests must be able to assert the same facts
without parsing secrets.

## Failure Contract

The smoke must exit non-zero and print a diagnostic when:

- Lima is unavailable;
- required helper binaries are unavailable;
- the configured target CLI is missing;
- tool supply fails;
- network preparation or endpoint reachability fails;
- unsafe workspace roots are accepted;
- native backend satisfies dogfood isolation by mistake;
- expected boundary decisions are missing from audit/summary; or
- secret values appear in routine evidence.

Failure must not silently fall back to native backend, host execution, ambient
host files, or ambient host networking.

## Evidence Contract

Evidence must include:

- session ID;
- environment ID;
- audit path;
- Boundary Summary;
- backend evidence label;
- network mode;
- reference workload result;
- known boundary-decision results.

Evidence must not include:

- full proxy URLs or proxy credentials;
- broker tokens;
- hidden endpoint internals;
- browser automation secrets;
- callback/open query secrets;
- raw host file contents except declared reference workspace artifacts.

## Boundary Action Set

The known set should include at least:

- one denied host.open to localhost/private or equivalent unsafe target;
- one HostFS reserved-root or denied access check;
- session lifecycle setup/end;
- network setup evidence;
- one endpoint exposure/preview.open event using an existing guest-loopback
  endpoint candidate.

The action set must be fixed by the smoke contract so SC-006 remains
measurable.
