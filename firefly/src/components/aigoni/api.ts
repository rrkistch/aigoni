export const base = "/api/admin/v1";
let csrf = "";
export class APIError extends Error {
 constructor(public status:number, public code:string, message:string, public fields:Record<string,string>={}) {super(message);}
}
export function setCSRF(token:string):void{csrf=token;}
export const idPath=(id:string):string=>id.split("/").map(encodeURIComponent).join("/");
export async function api(path:string, method="GET", body?:unknown, extra:Record<string,string>={}):Promise<any>{
 const headers:Record<string,string>={Accept:"application/json",...extra};
 if(method!=="GET")headers["X-CSRF-Token"]=csrf;
 if(body!==undefined && !(body instanceof FormData))headers["Content-Type"]="application/json";
 const response=await fetch(base+path,{method,credentials:"same-origin",headers,
 body:body===undefined?undefined:body instanceof FormData?body:JSON.stringify(body)});
 if(response.status===204)return null;
 const data=await response.json();
 if(!response.ok){
  if(response.status===401)window.dispatchEvent(new Event("aigoni-session-expired"));
  const e=data.error;
  throw new APIError(response.status,e?.code||"request_failed",typeof e==="string"?e:e?.message||"请求失败",e?.field_errors||{});
 }
 return data;
}
export async function eventStream(id:string,from:number,signal:AbortSignal,onEvent:(name:string,data:any)=>void):Promise<void>{
 const response=await fetch(base+"/wiki/runs/"+encodeURIComponent(id)+"/events?from="+from,{credentials:"same-origin",signal,headers:{Accept:"text/event-stream"}});
 if(!response.ok||!response.body)throw new Error("实时连接不可用，请重新连接");
 const reader=response.body.getReader();const decoder=new TextDecoder();let pending="";
 try {
  while(true){
   const chunk=await reader.read();if(chunk.done)break;
   pending+=decoder.decode(chunk.value,{stream:true});
   const frames=pending.split(/\r?\n\r?\n/);pending=frames.pop()||"";
   for(const frame of frames){
    const lines=frame.split(/\r?\n/);const name=lines.find(v=>v.startsWith("event:"))?.slice(6).trim()||"message";
    const raw=lines.filter(v=>v.startsWith("data:")).map(v=>v.slice(5).trimStart()).join("\n");
    if(raw)onEvent(name,JSON.parse(raw));
   }
  }
 } finally {reader.releaseLock();}
}
