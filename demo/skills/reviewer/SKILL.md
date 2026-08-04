---
name: reviewer
description: Review a change for the things a linter cannot see.
license: Apache-2.0
---

# Reviewing a change

Read the diff twice: once for what it does, and once for what it stops doing.

## What to look for

- Does the commit message say *why*? The diff already says what.
- Is there a test that fails without the change?
- Does anything here belong in a different change?
- What did this make harder for the next person?

## What not to do

Do not restate the diff. A review that says "this adds a function" has read the
change and understood nothing about it.
