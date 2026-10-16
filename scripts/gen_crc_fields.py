#!/usr/bin/env python3
"""Generate the seller app's CRC catalogue (bpp-frontend/src/crc.json) from ion-specs.

The BPP's Create Catalog page lists the Trade Catalogue Resource Categories
(CRCs) from ion-specs docs/ION_Resource_Categories.md, and its Add Product page
shows, for the catalog's CRC, the fields of the matching sub-object of
resourceAttributes in IONTradeResource
(releases/release1/schema/extension/trade/TradeResource/v1/attributes.yaml).
Re-run this after attributes are added to that schema.

$refs into other ion-specs files (vendored Beckn schemas, ...) are resolved
from the local checkout; allOf is merged. Each field becomes one of:
  text, number, boolean, select (options), multiselect (options),
  quantity (unitQuantity + unitCode), group (fields), list (rows of fields).

Usage:
  gen_crc_fields.py <ion-specs dir> <out.json>
"""
import json
import re
import sys

import yaml

BASE = "https://schema.ion.id/release1/"
RESOURCE = "releases/release1/schema/extension/trade/TradeResource/v1/attributes.yaml"
CATEGORIES = "docs/ION_Resource_Categories.md"

# CRC -> the IONTradeResource sub-objects whose fields it shows. From
# ION_Entity_Hierarchy_Model.md ("How a CRC connects to schema attribute
# packs"), plus the categories the schema also has a sub-object for. CRCs not
# listed show the common fields only.
SECTIONS = {
    "TRC-fashion": ["fashion"],
    "TRC-electronics": ["electronics"],
    "TRC-health-beauty": ["beauty", "pharmacy"],
    "TRC-food-bev": ["food", "agritech"],
    "TRC-digital": ["digital"],
}


class Resolver:
    def __init__(self, root):
        self.root = root
        self.docs = {}

    def load(self, path):
        if path not in self.docs:
            with open(f"{self.root}/{path}") as fh:
                self.docs[path] = yaml.safe_load(fh)
        return self.docs[path]

    def deref(self, ref, origin):
        target, _, frag = ref.partition("#")
        if target.startswith(BASE):
            path = "releases/release1/" + target[len(BASE):]
        elif target:
            path = target
        else:
            path = origin
        node = self.load(path)
        for tok in frag.lstrip("/").split("/"):
            if tok:
                node = node[tok.replace("~1", "/").replace("~0", "~")]
        return node, path

    def flatten(self, schema, origin, seen=()):
        """schema with $ref resolved and allOf merged (properties, required, ...)."""
        if not isinstance(schema, dict):
            return {}, origin
        if "$ref" in schema:
            ref = schema["$ref"]
            if ref in seen:
                return {}, origin
            target, path = self.deref(ref, origin)
            merged, _ = self.flatten(target, path, seen + (ref,))
            rest = {k: v for k, v in schema.items() if k != "$ref"}
            return self.merge(merged, self.flatten(rest, origin, seen)[0]), path
        out = {k: v for k, v in schema.items() if k != "allOf"}
        for part in schema.get("allOf", []):
            out = self.merge(out, self.flatten(part, origin, seen)[0])
        return out, origin

    @staticmethod
    def merge(a, b):
        out = dict(a)
        for k, v in b.items():
            if k == "properties":
                out["properties"] = {**out.get("properties", {}), **v}
            elif k == "required":
                out["required"] = sorted(set(out.get("required", [])) | set(v))
            elif k not in out:
                out[k] = v
        return out


# Words written in capitals in labels.
ACRONYMS = {"ram": "RAM", "os": "OS", "ip": "IP", "spf": "SPF", "sdppi": "SDPPI", "tkdn": "TKDN",
            "url": "URL", "uri": "URI", "sni": "SNI", "bpom": "BPOM"}
# Trailing unit words in keys, shown as "(unit)": chestCm -> "Chest (cm)".
UNITS = {"cm": "cm", "mm": "mm", "inch": "inch", "percentage": "%", "percent": "%", "kg": "kg", "g": "g"}


def label(key):
    """chestCm -> "Chest (cm)", screenSizeInch -> "Screen size (inch)", ram -> "RAM"."""
    words = re.sub(r"([a-z0-9])([A-Z])", r"\1 \2", key).replace("_", " ").lower().split()
    unit = UNITS.get(words[-1]) if len(words) > 1 else None
    if unit:
        words = words[:-1]
    words = [ACRONYMS.get(w, w) for w in words]
    text = " ".join(words)
    text = text[:1].upper() + text[1:]
    return f"{text} ({unit})" if unit else text


def own(schema, k):
    """schema's own k -- not one inherited through $ref / allOf from a shared
    type (Quantity's title "Quantity" is no label for a wattage field)."""
    if not isinstance(schema, dict):
        return None
    if schema.get(k):
        return schema[k]
    for part in schema.get("allOf", []):
        if isinstance(part, dict) and "$ref" not in part and part.get(k):
            return part[k]
    return None


def field(res, key, schema, origin, depth=0):
    s, origin = res.flatten(schema, origin)
    out = {"key": key, "label": own(schema, "title") or label(key)}
    help_text = own(schema, "description")
    if help_text:
        out["help"] = " ".join(str(help_text).split())
    props = s.get("properties") or {}
    t = s.get("type")
    if "enum" in s:
        out.update(kind="select", options=[str(o) for o in s["enum"]])
    elif t == "array":
        items, iorigin = res.flatten(s.get("items", {}), origin)
        if "enum" in items:
            out.update(kind="multiselect", options=[str(o) for o in items["enum"]])
        elif items.get("type") == "object" or items.get("properties"):
            out.update(kind="list", fields=fields(res, items, iorigin, depth + 1))
        else:
            out.update(kind="text", multiple=True)
    elif set(props) >= {"unitQuantity"} and depth < 3:
        out.update(kind="quantity")
    elif t == "object" or props:
        out.update(kind="group", fields=fields(res, s, origin, depth + 1))
    elif t in ("integer", "number"):
        out.update(kind="number", integer=t == "integer")
    elif t == "boolean":
        out.update(kind="boolean")
    else:
        out.update(kind="text")
        if s.get("format"):
            out["format"] = s["format"]
    if s.get("example") is not None and out["kind"] in ("text", "number"):
        out["example"] = s["example"]
    elif out["kind"] == "quantity" and isinstance(own(schema, "example"), dict):
        out["example"] = own(schema, "example")
    return out


def fields(res, schema, origin, depth=0):
    s, origin = res.flatten(schema, origin)
    required = set(s.get("required", []))
    out = []
    for key, sub in (s.get("properties") or {}).items():
        f = field(res, key, sub, origin, depth)
        if key in required:
            f["required"] = True
        out.append(f)
    return out


def categories(root):
    """The Trade sector's CRCs from ION_Resource_Categories.md."""
    with open(f"{root}/{CATEGORIES}") as fh:
        text = fh.read()
    trade = text.split("## Trade Sector", 1)[1].split("\n## ", 1)[0]
    out = []
    for line in trade.splitlines():
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        if len(cells) < 5 or not cells[1].startswith("`TRC-"):
            continue
        out.append({
            "code": cells[1].strip("`"),
            "name": cells[0],
            "status": cells[3],
            "description": cells[4],
        })
    return out


def main(root, out_path):
    res = Resolver(root)
    resource = res.load(RESOURCE)["components"]["schemas"]["IONTradeResource"]
    props = resource["properties"]
    crcs = categories(root)
    for crc in crcs:
        crc["sections"] = [
            {"key": key, "label": label(key), "help": " ".join(str(props[key].get("description", "")).split()),
             "fields": fields(res, props[key], RESOURCE)}
            for key in SECTIONS.get(crc["code"], []) if key in props
        ]
    doc = {
        "source": {"resource": RESOURCE, "categories": CATEGORIES},
        "crcField": "crc",
        "categories": crcs,
    }
    with open(out_path, "w") as fh:
        json.dump(doc, fh, indent=1, ensure_ascii=False)
        fh.write("\n")
    for crc in crcs:
        print(f"{crc['code']:<20} {crc['status']:<9} " + ", ".join(
            f"{s['key']}({len(s['fields'])})" for s in crc["sections"]))


if __name__ == "__main__":
    main(*sys.argv[1:])
