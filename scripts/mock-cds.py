import base64
import hashlib
import json
import os
import glob
import gzip
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

BECKN_DIR = "/beckn"


def sign_beckn_header(body: bytes, key_id: str, private_key_b64: str) -> str:
    """Builds a Beckn HTTP Signature Authorization header value, matching
    bap-application's becknAuthHeader / discovery-service's Signer.Sign
    exactly (same signing string, same digest prefix) so onix-bap's
    validateSign step accepts it.

    key_id must be "<subscriberId>|<uniqueKeyId>|ed25519" (BAP_KEY_ID as-is).
    private_key_b64 is a base64-encoded Ed25519 32-byte seed or 64-byte key.
    Mock-cds has no registered identity of its own on the real DeDi
    registry it's checked against, so -- same trick this repo's own
    DS_PRIVATE_KEY already uses for discovery-service locally -- it signs
    as the BAP's own already-registered identity instead of a fabricated,
    unresolvable one.
    """
    key_bytes = base64.b64decode(private_key_b64)
    seed = key_bytes[:32] if len(key_bytes) in (32, 64) else key_bytes
    private_key = Ed25519PrivateKey.from_private_bytes(seed)

    created = int(time.time())
    expires = created + 600

    digest_b64 = base64.b64encode(hashlib.blake2b(body, digest_size=64).digest()).decode()
    signing_string = f"(created): {created}\n(expires): {expires}\ndigest: BLAKE-512={digest_b64}"
    signature_b64 = base64.b64encode(private_key.sign(signing_string.encode())).decode()

    return (
        f'Signature keyId="{key_id}",algorithm="ed25519",created="{created}",'
        f'expires="{expires}",headers="(created) (expires) digest",signature="{signature_b64}"'
    )


def load_catalogs(text_search):
    """Loads every published catalog and narrows it to text_search, mirroring
    discovery-service's own textSearch matching (see its internal/service/match.go)
    closely enough for local dev/demo purposes."""
    catalogs = []
    catalog_files = glob.glob(os.path.join(BECKN_DIR, "catalogs", "*.latest.json.gz"))
    for f in catalog_files:
        try:
            with gzip.open(f, 'rt') as gz:
                data = json.load(gz)
                if 'catalog' in data:
                    catalogs.append(data['catalog'])
        except Exception as e:
            print(f"Error reading {f}: {e}")

    if not text_search:
        return catalogs

    filtered_catalogs = []
    for cat in catalogs:
        cat_name = cat.get("descriptor", {}).get("name", "").lower()
        if text_search in cat_name:
            filtered_catalogs.append(cat)
            continue

        matched_resources = []
        for res in cat.get("resources", []):
            res_name = res.get("descriptor", {}).get("name", "").lower()
            if text_search in res_name:
                matched_resources.append(res)

        if matched_resources:
            cat["resources"] = matched_resources
            filtered_catalogs.append(cat)
    return filtered_catalogs


class MockCDSHandler(BaseHTTPRequestHandler):
    def _read_json_body(self):
        content_length = int(self.headers.get('Content-Length', 0))
        post_data = self.rfile.read(content_length)
        try:
            return json.loads(post_data)
        except Exception:
            return {}

    def _respond_json(self, status, payload):
        body = json.dumps(payload).encode('utf-8')
        self.send_response(status)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        self.wfile.write(body)

    # GET /discover -- the same synchronous shortcut discovery-service's own
    # GET /discover offers (see discovery-service/README.md's sequence
    # diagram): answer with the full {context, message.catalogs} result
    # inline, no Ack/callback round trip. This is what bap-application's
    # DiscoverSync (DISCOVER_METHOD=GET) calls directly, bypassing onix-bap
    # entirely -- so none of onix-bap's Ack-signature or inbound
    # validateSign/DeDi-registry checks ever apply to this path.
    def do_GET(self):
        req = self._read_json_body()
        context = dict(req.get("context", {}))
        context["action"] = "on_discover"

        text_search = req.get("message", {}).get("intent", {}).get("textSearch", "").strip().lower()
        catalogs = load_catalogs(text_search)

        self._respond_json(200, {"context": context, "message": {"catalogs": catalogs}})

    # POST /discover -- the real async Beckn v2 contract: an immediate Ack,
    # then a separate on_discover callback delivered later. Used when
    # bap-application's Discover goes through onix-bap's bapTxnCaller
    # (DISCOVER_METHOD=POST).
    def do_POST(self):
        req = self._read_json_body()

        bap_uri = req.get("context", {}).get("bapUri")
        action = req.get("context", {}).get("action")

        self._respond_json(200, {
            "context": req.get("context", {}),
            "message": {"ack": {"status": "ACK"}},
        })

        if not bap_uri:
            print("No bapUri provided, cannot send async callback")
            return

        def send_callback(callback_req, cb_bap_uri, cb_action):
            time.sleep(1)  # simulate network/processing delay

            callback_payload = {
                "context": dict(callback_req.get("context", {})),
                "message": {},
            }
            callback_payload["context"]["action"] = "on_" + cb_action

            # bppId/bppUri must match whatever identity actually signs this
            # callback below, not the reference app's real BPP_ID -- onix-bap
            # cross-checks the two ("subscriber identity mismatch" otherwise).
            # Real discovery-service does the same: it stamps its OWN
            # SubscriberID/SubscriberURI here (see
            # discovery-service/internal/config.Config.SubscriberID's own
            # comment), never a copy of this reference app's BPP identity.
            # Since mock-cds signs as the BAP (see sign_beckn_header's own
            # comment -- it has no independently-registered identity of its
            # own), it must declare that same BAP identity here too.
            bap_key_id = os.environ.get("BAP_KEY_ID", "")
            callback_payload["context"]["bppId"] = bap_key_id.split("|")[0] if bap_key_id else "mock-cds"
            callback_payload["context"]["bppUri"] = cb_bap_uri

            if cb_action == "discover":
                text_search = callback_req.get("message", {}).get("intent", {}).get("textSearch", "").strip().lower()
                callback_payload["message"]["catalogs"] = load_catalogs(text_search)
            else:
                print(f"Mock CDS only handles discover. Unsupported action: {cb_action}")
                return

            # Always deliver to the requester's own bapUri -- NOT
            # BPP_CALLER_URL. That env var routes to onix-bpp's caller
            # module, which is wrong here: mock-cds is standing in for a
            # Discovery Service answering the BAP directly, the same
            # relationship the real discovery-service has (see its
            # dispatch package), not a BPP replying through its own
            # adapter's caller.
            target_url = cb_bap_uri + "/on_" + cb_action

            body = json.dumps(callback_payload).encode('utf-8')
            headers = {'Content-Type': 'application/json'}

            bap_key_id = os.environ.get("BAP_KEY_ID")
            bap_private_key = os.environ.get("BAP_PRIVATE_KEY")
            if bap_key_id and bap_private_key:
                headers['Authorization'] = sign_beckn_header(body, bap_key_id, bap_private_key)
            else:
                print("BAP_KEY_ID/BAP_PRIVATE_KEY not set, sending on_discover callback unsigned")

            try:
                req_obj = urllib.request.Request(
                    target_url,
                    data=body,
                    headers=headers,
                    method='POST'
                )
                with urllib.request.urlopen(req_obj, timeout=30) as response:
                    print(f"Callback sent to {target_url}, response: {response.getcode()}")
            except Exception as e:
                print(f"Failed to send on_{cb_action} callback: {e}")

        threading.Thread(target=send_callback, args=(req, bap_uri, action)).start()


if __name__ == '__main__':
    # Local development only: the mock is reached inside the compose network,
    # with TLS handled nowhere else in the local stack either.
    server = HTTPServer(('0.0.0.0', 8086), MockCDSHandler)  # NOSONAR -- local mock, plain HTTP by design
    print('Starting mock CDS on port 8086...')
    server.serve_forever()
