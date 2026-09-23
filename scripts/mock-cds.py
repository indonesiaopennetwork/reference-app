import json
import os
import glob
import gzip
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

BECKN_DIR = "/beckn"

class MockCDSHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.handle_request()
    
    def do_POST(self):
        self.handle_request()

    def handle_request(self):
        content_length = int(self.headers.get('Content-Length', 0))
        post_data = self.rfile.read(content_length)
        
        try:
            req = json.loads(post_data)
        except:
            req = {}
            
        bap_uri = req.get("context", {}).get("bapUri")
        transaction_id = req.get("context", {}).get("transactionId")
        message_id = req.get("context", {}).get("messageId")
        action = req.get("context", {}).get("action")

        # Send immediate ACK
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        ack = {
            "context": req.get("context", {}),
            "message": {"ack": {"status": "ACK"}}
        }
        self.wfile.write(json.dumps(ack).encode('utf-8'))
        
        if not bap_uri:
            print("No bapUri provided, cannot send async callback")
            return

        def send_callback(callback_req, cb_bap_uri, cb_txn_id, cb_msg_id, cb_action):
            time.sleep(1) # simulate network/processing delay
            
            callback_payload = {
                "context": callback_req.get("context", {}),
                "message": {}
            }
            callback_payload["context"]["action"] = "on_" + cb_action
            callback_payload["context"]["bppId"] = os.environ.get("BPP_ID", "mock-cds")
            callback_payload["context"]["bppUri"] = os.environ.get("BPP_URI", "http://mock-cds:8086")

            if cb_action == "discover":
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
                
                text_search = callback_req.get("message", {}).get("intent", {}).get("textSearch", "").strip().lower()
                if text_search:
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
                    catalogs = filtered_catalogs
                callback_payload["message"]["catalogs"] = catalogs

            else:
                print(f"Mock CDS only handles discover. Unsupported action: {cb_action}")
                return

            bpp_caller_url = os.environ.get("BPP_CALLER_URL")
            target_url = (bpp_caller_url + "/on_" + cb_action) if bpp_caller_url else (cb_bap_uri + "/on_" + cb_action)

            try:
                req_obj = urllib.request.Request(
                    target_url, 
                    data=json.dumps(callback_payload).encode('utf-8'),
                    headers={'Content-Type': 'application/json'},
                    method='POST'
                )
                with urllib.request.urlopen(req_obj, timeout=30) as response:
                    print(f"Callback sent to {target_url}, response: {response.getcode()}")
            except Exception as e:
                print(f"Failed to send on_{cb_action} callback: {e}")

        threading.Thread(target=send_callback, args=(req, bap_uri, transaction_id, message_id, action)).start()

if __name__ == '__main__':
    server = HTTPServer(('0.0.0.0', 8086), MockCDSHandler)
    print('Starting mock CDS on port 8086...')
    server.serve_forever()
