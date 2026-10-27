"""Serve a folder laid out as a repository's GitHub releases are, on this
machine's loopback address, for the install step of scripts/platform-smoke.sh.

    python3 -I -u scripts/lib/serve-release-folder.py <root> <tag> <port-file>

<root> holds download/<tag>/<file>. A request for /download/<tag>/<file> is
answered with that file, and a request for /latest/download/<file> with a 302
redirect to /download/<tag>/<file>, the way GitHub answers for a repository's
latest release. Every file is sent as application/octet-stream, as GitHub sends
the files attached to a release. Anything else is answered 404.

It listens on 127.0.0.1 alone, on a port the system picks, and writes that port
into <port-file> once it is listening — under another name first, then renamed,
so a port file that is present is complete. It binds without looking the address
up for a host name, as http.server's own server does: that lookup asks the
machine's resolver, which can take longer to answer than the smoke run waits for
the port, and this server, which answers on the address itself, needs no name.

On standard error it writes a line when it starts, before it binds, and a line
once it is listening, then each request. Where the platform has SIGUSR1, that
signal writes every thread's stack there, so a server that has not named its port
when the smoke run stops waiting shows where it is. It runs until it is stopped.
It needs Python 3.7 or later and nothing beyond the standard library.
"""

import faulthandler
import functools
import http.server
import os
import signal
import socketserver
import sys

USAGE = "usage: serve-release-folder.py <root> <tag> <port-file>\n"
LATEST = "/latest/download/"
DOWNLOAD = "/download/"


class ReleaseHandler(http.server.SimpleHTTPRequestHandler):
    tag = ""

    def guess_type(self, path):
        return "application/octet-stream"

    def send_head(self):
        path = self.path.split("?", 1)[0].split("#", 1)[0]
        if path.startswith(LATEST) and "/" not in path[len(LATEST):]:
            host, port = self.server.server_address[:2]
            self.send_response(302)
            self.send_header(
                "Location",
                "http://%s:%d%s%s/%s" % (host, port, DOWNLOAD, self.tag, path[len(LATEST):]),
            )
            self.send_header("Content-Length", "0")
            self.end_headers()
            return None
        if not path.startswith(DOWNLOAD) or path.endswith("/"):
            self.send_error(404)
            return None
        return super().send_head()


class LoopbackServer(http.server.ThreadingHTTPServer):
    def server_bind(self):
        # http.server.HTTPServer.server_bind binds, then sets server_name from
        # socket.getfqdn — a lookup through the machine's resolver. Bind as
        # socketserver does and name the server by its address instead.
        socketserver.TCPServer.server_bind(self)
        host, port = self.server_address[:2]
        self.server_name = host
        self.server_port = port


def main(argv):
    if len(argv) != 4 or not all(argv[1:]):
        sys.stderr.write(USAGE)
        return 2
    if hasattr(signal, "SIGUSR1"):
        faulthandler.register(signal.SIGUSR1, all_threads=True)
    root, tag, port_file = argv[1:]
    sys.stderr.write(
        "serve-release-folder: Python %s (%s) starting, pid %d\n"
        % (sys.version.split()[0], sys.executable, os.getpid())
    )
    sys.stderr.flush()
    if not os.path.isdir(os.path.join(root, "download", tag)):
        sys.stderr.write("serve-release-folder: %s holds no download/%s\n" % (root, tag))
        return 1
    handler = type("Handler", (ReleaseHandler,), {"tag": tag})
    server = LoopbackServer(("127.0.0.1", 0), functools.partial(handler, directory=root))
    port = server.server_address[1]
    partial = port_file + ".partial"
    with open(partial, "w") as f:
        f.write("%d\n" % port)
    os.replace(partial, port_file)
    sys.stderr.write("serve-release-folder: serving %s on 127.0.0.1:%d\n" % (root, port))
    sys.stderr.flush()
    server.serve_forever()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
