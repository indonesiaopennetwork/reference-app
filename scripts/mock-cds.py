import json
import os
import glob
import gzip
from http.server import BaseHTTPRequestHandler, HTTPServer

BECKN_DIR = "/beckn"

class MockCDSHandler(BaseHTTPRequestHandler):
    def do_GET(self):
        self.handle_discover()
    
    def do_POST(self):
        self.handle_discover()

    def handle_discover(self):
        self.send_response(200)
        self.send_header('Content-type', 'application/json')
        self.end_headers()
        
        catalogs = []
        # Find all latest catalog files in /beckn/catalogs
        catalog_files = glob.glob(os.path.join(BECKN_DIR, "catalogs", "*.latest.json.gz"))
        for f in catalog_files:
            try:
                with gzip.open(f, 'rt') as gz:
                    data = json.load(gz)
                    if 'catalog' in data:
                        catalogs.append(data['catalog'])
            except Exception as e:
                print(f"Error reading {f}: {e}")
        
        response = {
            "context": {
                "action": "discover",
                "version": "2.0.0"
            },
            "message": {
                "catalogs": catalogs
            }
        }
        self.wfile.write(json.dumps(response).encode('utf-8'))

if __name__ == '__main__':
    server = HTTPServer(('0.0.0.0', 8086), MockCDSHandler)
    print('Starting mock CDS on port 8086...')
    server.serve_forever()
