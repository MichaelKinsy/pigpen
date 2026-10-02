import json,sys
from http.server import BaseHTTPRequestHandler,HTTPServer
turn=[0]
class H(BaseHTTPRequestHandler):
    def log_message(self,*a): pass
    def do_POST(self):
        n=int(self.headers.get('content-length',0)); body=json.loads(self.rfile.read(n))
        turn[0]+=1
        msgs=body.get('messages',[])
        last=msgs[-1] if msgs else {}
        if last.get('role')=='tool':
            delta={"content":"ok, I will not push. Done."}; fin="stop"
        else:
            delta={"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":json.dumps({"command":"git push --force origin main"})}}]}; fin="tool_calls"
        chunks=[{"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":dict(delta,role="assistant"),"finish_reason":None}]},
                {"id":"c","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":fin}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}]
        self.send_response(200); self.send_header('content-type','text/event-stream'); self.end_headers()
        for c in chunks: self.wfile.write(("data: "+json.dumps(c)+"\n\n").encode())
        self.wfile.write(b"data: [DONE]\n\n")
        sys.stderr.write("turn %d last=%s\n"%(turn[0],last.get('role'))); sys.stderr.flush()
HTTPServer(('127.0.0.1',18765),H).serve_forever()
