# Tasks: First-Run Documentation Path

<!-- markdownlint-disable MD013 -->

**Input**: Design documents from `specs/020-first-run-documentation-path/`

## Phase 1: Setup

- [X] T001 Add 020 spec/checklist/plan/research/data-model/contracts/quickstart/tasks artifacts

## Phase 2: Implementation

- [X] T002 Add canonical external-alpha first-run page in docs/first-run-alpha.md
- [X] T003 Link first-run page from README.md and docs/README.md
- [X] T004 Update docs/STATUS.md with first-run path status
- [X] T005 Update docs/privacy-run-test-plan.md with first-run docs smoke
- [X] T006 Add scripts/test-first-run-docs-smoke.sh
- [X] T007 Wire first-run docs smoke into scripts/test-gate0.sh

## Phase 3: Verification

- [X] T008 Run scripts/test-first-run-docs-smoke.sh
- [X] T009 Run markdownlint for README, docs, and specs/020
- [X] T010 Run git diff --check
