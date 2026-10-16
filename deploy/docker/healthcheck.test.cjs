const {test}=require("node:test");
const assert=require("node:assert/strict");
const http=require("node:http");
const {spawn}=require("node:child_process");
const path=require("node:path");

for(const [label,status,body,expected] of [
 ["healthy API during initial build",200,'{"version":"v1"}',0],
 ["unavailable API",503,'{"error":"not ready"}',1],
 ["wrong endpoint",200,'{"version":"other"}',1],
 ["invalid JSON",200,'not JSON',1],
]){
 test(label,async()=>{
  const server=http.createServer((req,res)=>{
   assert.equal(req.url,"/rest/v1");
   res.writeHead(status,{"Content-Type":"application/json"});res.end(body);
  });
  await new Promise(resolve=>server.listen(0,"127.0.0.1",resolve));
  try{
   const child=spawn(process.execPath,[path.join(__dirname,"healthcheck.cjs")],{
    env:{...process.env,PORT:String(server.address().port)},stdio:"ignore",
   });
   const code=await new Promise((resolve,reject)=>{child.once("exit",resolve);child.once("error",reject)});
   assert.equal(code,expected);
  }finally{await new Promise(resolve=>server.close(resolve));}
 });
}
