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

export async function uploadMedia(file:File,onProgress:(percent:number)=>void):Promise<any>{return uploadFile("/media/uploads","aigoni-media-upload:",file,onProgress);}
export async function uploadBackup(file:File,onProgress:(percent:number)=>void):Promise<any>{return uploadFile("/backups/uploads","aigoni-backup-upload:",file,onProgress);}
export async function uploadTheme(file:File,onProgress:(percent:number)=>void,signal?:AbortSignal,onState?:(value:any)=>void):Promise<any>{
 const tag=file.name.match(/^aigoni-(theme-v[0-9]+\.[0-9]+\.[0-9]+)-linux-amd64\.tar\.gz$/)?.[1];
 if(!tag)throw new Error("请选择本项目发布的 aigoni-theme-v版本-linux-amd64.tar.gz 文件。");
 return uploadFile("/theme/uploads","aigoni-theme-upload:",file,onProgress,{tag},signal,onState);
}
async function uploadFile(endpoint:string,prefix:string,file:File,onProgress:(percent:number)=>void,extra:Record<string,string>={},signal?:AbortSignal,onState?:(value:any)=>void):Promise<any>{
 const key=prefix+file.name+":"+file.size+":"+file.lastModified;
 let upload:any=null;const prior=sessionStorage.getItem(key);
 if(prior){try{upload=await api(endpoint+"/"+prior);}catch{sessionStorage.removeItem(key);}}
 if(!upload){upload=await api(endpoint,"POST",{name:file.name,size:file.size,...extra});sessionStorage.setItem(key,upload.id);}
 onState?.(upload);
 while(upload.offset<file.size){
  const end=Math.min(upload.offset+8*1024*1024,file.size);
  const response=await fetch(base+endpoint+"/"+upload.id+"?offset="+upload.offset,{method:"PUT",credentials:"same-origin",headers:{"X-CSRF-Token":csrf,"Content-Type":"application/octet-stream"},body:file.slice(upload.offset,end),signal});
  if(response.status===401)window.dispatchEvent(new Event("aigoni-session-expired"));
  let data;try{data=await response.json();}catch{throw new Error(`上传返回 HTTP ${response.status}，请检查反向代理上传限制或重新连接。`);}
  if(!response.ok)throw new Error(data.error?.message||"上传失败，请重新选择同一文件继续。");upload=data;onProgress(Math.round(upload.offset/file.size*100));
 }
 const result=await api(endpoint+"/"+upload.id+"/complete","POST");sessionStorage.removeItem(key);return result;
}
