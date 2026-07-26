# Vendored `@gaarutyunov/ui-kit`

Source: [gaarutyunov/ui-kit](https://github.com/gaarutyunov/ui-kit)
Pinned at **v0.2.0** (`91d014bfe11e275286b1f93dc9bef71ea27b97a6`).

## Why vendored rather than installed

The kit publishes to **GitHub Packages**, which requires authentication even for
public packages. A workflow's `GITHUB_TOKEN` is scoped to *this* repository, so
it cannot read a package owned by another one — `npm ci` would need a
long-lived PAT in repository secrets just to install a zero-dependency set of
web components. `gaarutyunov/gopgql` vendors it for the same reason.

The kit is dependency-free ES modules and CSS, so vendoring costs nothing at
build time and keeps the docs build reproducible with no secrets.

## Updating

```bash
git -C <path-to>/ui-kit archive vX.Y.Z src LICENSE README.md \
  | tar -x -C docs/vendor/ui-kit
```

Then update the version and commit above.
