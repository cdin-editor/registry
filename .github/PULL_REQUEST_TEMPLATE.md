## Summary

<!-- Describe the behavior changed. Keep this PR focused on one concern. -->

## Why is this needed?

<!-- Link an issue or explain the problem this solves. -->

## Registry area

- [ ] CLI / command behavior
- [ ] Product Definition / input validation
- [ ] Catalog ingestion / metadata integrity
- [ ] Version and dependency resolution
- [ ] Contract compatibility
- [ ] Git source collection
- [ ] Workspace / manifest / report
- [ ] Build / CI / release automation
- [ ] Documentation / repository governance

## Specification references

<!-- Cite the accepted REG requirement(s), ADR or exact Spec document. -->
- Requirement IDs:
- ADR / contract version:

## Design and compatibility

- [ ] Registry remains consumer-independent; no product-specific branch or plug-in was added.
- [ ] Component repository topology is not inferred from repository names.
- [ ] External metadata is treated as untrusted data.
- [ ] Existing manifests and workspaces are not silently rewritten.
- [ ] Any public-contract change was proposed in cdin-editor/spec first.

Breaking changes, trade-offs and deferred work:

<!-- Write "None" when not applicable. -->

## Verification

- [ ] `pipeline run check --json` passes.
- [ ] `psx check` passes.
- [ ] Schema mirror check passes against the pinned Spec revision.
- [ ] Positive and negative tests cover the changed behavior.
- [ ] Failure cleanup and path-safety cases are tested where relevant.
- [ ] Documentation and CHANGELOG.md are updated.

Commands and results:

```text
<!-- Paste the actual commands and results. Do not claim checks that were not run. -->
```

## Review checklist

- [ ] I reviewed the complete diff.
- [ ] No secrets, credentials, local paths or generated build output were committed.
- [ ] Limitations and untested platforms are stated explicitly.
