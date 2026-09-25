# FaultLine proof bundle

Mode: RERUN
Fixture: Parallel settlement-currency regression
Witness: sha256:3d0d826bb1fd9901b290475201303c560a961df34e2faa374cc66310cb70327c

## Bounded conclusion

The earliest stable recorded transition for the frozen sample witness is cedar-turn-4 -> cedar-turn-5.

This bundle supports a predicate-specific result. It does not establish model intent, semantic root cause, or a unique minimal cause.

## Prevention proof

- Last-good: PASS
- First-bad: FAIL
- Repaired: PASS

## Integrity boundary

`fl verify` checks the complete declared file set without running repository code. Pass an externally recorded bundle root with `--expect-root` to detect an editor who updates the mutable hash list too.