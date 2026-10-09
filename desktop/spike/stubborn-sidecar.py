#!/usr/bin/env python3
# Test double: answers the readiness probe, then ignores stdin EOF, the shutdown command and SIGTERM.
import http.server, json, os, signal, sys, threading, time
signal.signal(signal.SIGTERM, signal.SIG_IGN)
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200); self.send_header("X-Gleam-Server", "gleam"); self.send_header("Content-Type", "text/html")
        self.end_headers(); self.wfile.write(b"<!doctype html><title>stub</title><p>stub")
    def log_message(self, *a): pass
srv = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
threading.Thread(target=srv.serve_forever, daemon=True).start()
print("GLEAM_READY " + json.dumps({"addr": "127.0.0.1:%d" % srv.server_address[1], "pid": os.getpid(), "version": "stub"}), flush=True)
os.system("sleep 600 &")  # a grandchild, to check the process-group kill
while True: time.sleep(1)
