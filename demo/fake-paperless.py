#!/usr/bin/env python3
"""
A tiny stand-in for a Paperless-ngx server, only for the demo recording: it
answers the handful of API calls doxie-scanner makes (tag / correspondent /
document-type lists, document upload, task status) so "Send to Paperless" can
be shown for real, end to end, without a Paperless install. It stores nothing.

Usage: python3 fake-paperless.py <port>
"""
import json
import sys
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

LISTS = {
    "/api/tags/": [{"id": 1, "name": "Receipts"}, {"id": 2, "name": "Taxes"}, {"id": 3, "name": "Business"}],
    "/api/correspondents/": [{"id": 7, "name": "Rivertown Supply"}, {"id": 8, "name": "Springvale Utilities"}],
    "/api/document_types/": [{"id": 3, "name": "Receipt"}, {"id": 4, "name": "Statement"}],
}
UPLOADED_AT = {}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("X-Api-Version", "9")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path, _, query = self.path.partition("?")
        if path in LISTS:
            return self._json({"count": len(LISTS[path]), "next": None, "results": LISTS[path]})
        if path == "/api/tasks/":
            task = query.split("task_id=")[-1]
            # Report "still working" for a moment, then filed, like the real thing.
            done = time.time() - UPLOADED_AT.get(task, 0) > 2
            return self._json([{"task_id": task, "status": "SUCCESS" if done else "STARTED",
                                "related_document": "42" if done else None}])
        return self._json({"count": 0, "next": None, "results": []})

    def do_POST(self):
        self.rfile.read(int(self.headers.get("Content-Length", 0)))
        UPLOADED_AT["demo-task"] = time.time()
        body = b'"demo-task"'
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


HTTPServer(("0.0.0.0", int(sys.argv[1])), Handler).serve_forever()
