import fs from "node:fs/promises";
import path from "node:path";
import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)),"..");
const [input,output]=process.argv.slice(2);
if(!input||!output)throw new Error("Expected snapshot and release directory");
const snapshot=JSON.parse(await fs.readFile(input,"utf8"));
const generation=path.basename(path.resolve(output));
if(!/^[\w-]+$/.test(generation))throw new Error("Invalid release ID");
const contentRoot=path.join(root,".aigoni",generation);
for(const kind of ["posts","pages"]){
 await fs.mkdir(path.join(contentRoot,kind),{recursive:true});
 for(const item of snapshot[kind]){
  if(!/^[a-zA-Z0-9_-]+$/.test(item.slug))throw new Error("Unsafe slug");
  const fields={title:item.title,description:item.description,category:item.category,tags:item.tags,image:item.image,aigoniWeight:item.weight,aigoniTOC:item.toc,draft:false};
  const front=Object.entries(fields).map(([k,v])=>k+": "+JSON.stringify(v)).join("\n");
  await fs.writeFile(path.join(contentRoot,kind,item.slug+".md"),"---\n"+front+"\npublished: "+item.published+"\nupdated: "+item.updated+"\n---\n"+item.body+"\n");
 }
}
const env={...process.env,AIGONI_CONTENT_DIR:path.relative(root,contentRoot).replaceAll("\\","/"),AIGONI_ASSET_PREFIX:"/_firefly/"+generation,PUBLIC_AIGONI_SITE:JSON.stringify({...snapshot.site,adminPath:snapshot.adminPath}),ASTRO_TELEMETRY_DISABLED:"1"};
delete env.CF_WORKERS;
await new Promise((resolve,reject)=>{
 const child=spawn(process.platform==="win32"?"pnpm.cmd":"pnpm",["run","build"],{cwd:root,env,stdio:"inherit",shell:process.platform==="win32"});
 child.once("error",reject);child.once("exit",code=>code===0?resolve():reject(new Error("Build exited "+code)));
});
await fs.access(path.join(root,"dist","_workspace","index.html"));
await fs.cp(path.join(root,"dist"),path.resolve(output),{recursive:true});
console.log("Firefly release ready:",generation);
