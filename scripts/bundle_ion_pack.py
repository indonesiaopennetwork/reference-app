#!/usr/bin/env python3
"""Bundle an ION OpenAPI document from a local ion-specs tree into one file.

The onix adapters validate against ION documents as published on
schema.ion.id. Before an ion-specs change is published, a local copy can be
used instead (schemas/README.md) -- but a plain copy still resolves its $refs
to other ION packs on schema.ion.id, i.e. their published versions. This
copies every component the document reaches in *other ION packs* from the
local ion-specs tree into its own components and makes those $refs local.
$refs into the vendored Beckn documents stay remote (they don't change).

Usage:
  bundle_ion_pack.py <ion-specs release dir> <document> <out.yaml>

<document> is a path below the release dir, e.g.
  schema/common/Settlement/v1/attributes.yaml
  schema/core/api/v2.0.0/ion-additional.yaml
"""
import copy
import sys

import yaml

BASE = "https://schema.ion.id/release1/"
VENDORED = BASE + "schema/vendored/"


class Bundler:
    def __init__(self, release_dir):
        self.release_dir = release_dir
        self.docs = {}

    def load(self, path):
        if path not in self.docs:
            with open(f"{self.release_dir}/{path}") as fh:
                self.docs[path] = yaml.safe_load(fh)
        return self.docs[path]

    def resolve(self, path, pointer):
        node = self.load(path)
        for token in pointer.lstrip("/").split("/"):
            node = node[token.replace("~1", "/").replace("~0", "~")]
        return node

    def bundle(self, entry):
        doc = copy.deepcopy(self.load(entry))
        schemas = doc.setdefault("components", {}).setdefault("schemas", {})
        own = set(schemas)  # the entry document's own components
        done = {}

        def localize(ref, origin):
            target, _, frag = ref.partition("#")
            if target.startswith(VENDORED):
                return ref
            if target and not target.startswith(BASE):
                return ref  # another site: left as it is
            path = target[len(BASE):] if target else origin
            if path == entry and frag.startswith("/components/schemas/") and frag.count("/") == 3:
                return "#" + frag
            parts = frag.lstrip("/").split("/")
            if parts[:2] == ["components", "schemas"] and len(parts) == 3:
                name = parts[2]
                local = f"#/components/schemas/{name}"
                if (path, name) not in done:
                    if name in schemas and path != entry:
                        raise SystemExit(f"component {name} of {path} collides with one already in the bundle")
                    done[(path, name)] = True
                    if path != entry:
                        schemas[name] = None  # reserve before walking: refs may cycle
                        schemas[name] = walk(copy.deepcopy(self.resolve(path, frag)), path)
                return local
            # A pointer into a component's properties: inline it.
            return {"inline": walk(copy.deepcopy(self.resolve(path, frag)), path)}

        def walk(node, origin):
            if isinstance(node, dict):
                out = {}
                for k, v in node.items():
                    if k == "$ref" and isinstance(v, str):
                        r = localize(v, origin)
                        if isinstance(r, dict):
                            out.update(r["inline"])
                            continue
                        out[k] = r
                    else:
                        out[k] = walk(v, origin)
                return out
            if isinstance(node, list):
                return [walk(v, origin) for v in node]
            return node

        for key in ("paths",):
            if key in doc:
                doc[key] = walk(doc[key], entry)
        for name in list(own):
            schemas[name] = walk(schemas[name], entry)
        return doc


def main(release_dir, document, out_path):
    doc = Bundler(release_dir).bundle(document)
    with open(out_path, "w") as fh:
        fh.write(f"# Bundled from ion-specs releases/release1/{document}\n"
                 "# by scripts/bundle_ion_pack.py: components of other ION packs are\n"
                 "# inlined from the local ion-specs tree; vendored Beckn refs stay remote.\n"
                 "# Do not edit by hand; re-run the script (see schemas/README.md).\n")
        yaml.safe_dump(doc, fh, sort_keys=False, allow_unicode=True, width=100)


if __name__ == "__main__":
    if len(sys.argv) != 4:
        raise SystemExit(__doc__)
    main(*sys.argv[1:])
