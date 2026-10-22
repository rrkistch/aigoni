<script lang="ts">
 import {onMount} from "svelte";
 import {api,base,uploadBackup} from "./api";
 let items=$state<any[]>([]);let health=$state<any>(null);let recovery=$state(false);let maintenance=$state("");let busy=$state(false);let progress=$state(0);let error=$state("");let message=$state("");
 const active=(item:any)=>["queued","creating","validating","queued-restore","safety-backup","restoring"].includes(item.status);
 const label=(status:string)=>({queued:"等待备份",creating:"正在创建备份",ready:"可下载",uploading:"上传中",validating:"正在验证",validated:"已验证，等待确认","queued-restore":"等待恢复","safety-backup":"正在保存恢复前备份",restoring:"正在恢复",restored:"已恢复",failed:"未完成"}[status]||status);
 const names:Record<string,string>={posts:"文章",pages:"页面",notes:"私人笔记",dynamics:"动态",projects:"项目",public:"站点资源",uploads:"上传文件",wiki:"知识库",config:"站点设置",theme:"外观与模块设置",onboarding:"建站进度",history:"设置历史",mediafiles:"公开素材文件",mediaindex:"素材目录"};
 async function load(){const data=await api("/backups");items=data.items;recovery=data.recovery_required;maintenance=data.maintenance_url;if(!recovery&&!items.some(active))health=await api("/health");}
 onMount(()=>{void load().catch(e=>error=e.message);const timer=setInterval(()=>{if(items.some(active))void load().catch(e=>error=e.message);},2000);return()=>clearInterval(timer);});
 async function action(fn:()=>Promise<void>){busy=true;error="";message="";try{await fn();await load();}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function create(){if(!confirm("备份会包含私人笔记和 Wiki，请安全保管下载文件。创建期间短暂暂停内容保存，是否继续？"))return;await action(async()=>{await api("/backups","POST",{});message="备份已排队，完成后可下载。";});}
 async function upload(e:Event){const file=(e.currentTarget as HTMLInputElement).files?.[0];if(!file)return;progress=0;await action(async()=>{await uploadBackup(file,p=>progress=p);message="上传完成，正在验证备份；验证不会修改当前网站。";});}
 async function restore(item:any){if(!confirm("恢复会替换当前文章、私人笔记、Wiki、素材和站点设置。备份中为空的分区会清空当前对应数据。系统会先保留恢复前备份；AI 连接、密钥和助手授权不会被覆盖。确认恢复？"))return;await action(async()=>{await api(`/backups/items/${item.id}/restore`,"POST",{confirm:true});message="恢复任务已排队，完成后网站会重新构建。";});}
 async function validate(item:any){await action(async()=>{await api(`/backups/items/${item.id}/validate`,"POST",{});message="正在重新验证。";});}
 async function remove(item:any){if(!confirm("删除服务器上的这份备份/恢复包及其暂存数据？不会删除当前网站内容。"))return;await action(async()=>{await api(`/backups/items/${item.id}`,"DELETE");});}
</script>
<section class="card-base panel stack"><h1>网站维护</h1>
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}{#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if recovery}<div class="notice error">存在未完成的数据恢复，正常读写已暂停。<a href={maintenance} data-no-swup>进入独立维护入口</a></div>{/if}
 {#if health}<h2>站点检查</h2>{#each health.checks as check}<div class="item"><div class="grow"><strong>{check.label}</strong><p class="muted">{check.message}</p></div><span class="pill">{check.status==="ok"?"正常":"需要留意"}</span></div>{/each}<p class="muted">数据目录可用 {(health.free_bytes/1024/1024/1024).toFixed(2)} GiB · 主题 {health.theme.version} · AI {health.ai_configured?"已配置（未自动调用测试）":"未配置"}</p>{/if}
 <div class="row spread"><h2>备份与恢复</h2><button disabled={busy} onclick={()=>load().catch(e=>error=e.message)}>刷新</button></div>
 <p class="muted">备份包含私人数据，只恢复你信任的本站备份。不包含 AI 连接、API 密钥、登录会话或助手授权；原机恢复保留它们，新机器需另行配置。主题程序和构建依赖也不在数据备份中。</p>
 <div class="row"><button class="primary" disabled={busy||recovery||items.some(active)} onclick={create}>创建数据备份</button><label class="upload">上传恢复包<input type="file" accept=".tar.gz" disabled={busy||recovery||items.some(active)} onchange={upload} /></label></div>
 {#if busy&&progress>0}<progress max="100" value={progress} aria-label="备份上传进度"></progress><p role="status">已上传 {progress}%</p>{/if}
 <p class="muted">上传分块为 8 MiB，恢复包最大 4 GiB。验证通过后仍需你确认才会恢复；沿用当前部署的数据目录位置。外部同步脚本请先暂停写入。</p>
 {#each items as item}<article class="stack backup-item"><div class="row spread"><strong>{item.name||new Date(item.created).toLocaleString()}</strong><span class="pill">{label(item.status)}</span></div><small class="muted">{item.id} · {(item.size/1024/1024).toFixed(2)} MiB</small>
 {#if item.error}<p class="notice error">{item.error}</p>{/if}
 {#if item.manifest}<details><summary>查看备份内容与恢复影响</summary><p>备份时间：{new Date(item.manifest.created).toLocaleString()} · 原始数据 {(item.manifest.bytes/1024/1024).toFixed(2)} MiB</p><ul>{#each Object.entries(item.manifest.roots) as [name,present]}<li>{names[name]||name}：{present?"有备份内容":"为空；恢复会清空当前对应分区"}</li>{/each}</ul></details>{/if}
 {#if item.safety_backup}<p class="muted">恢复前备份：{item.safety_backup}</p>{/if}
 <div class="row">{#if item.kind==="backup"&&item.status==="ready"}<a class="button" data-no-swup href={`${base}/backups/items/${item.id}/download`}>下载备份</a>{/if}
 {#if item.kind==="restore"&&item.status==="validated"}<button class="primary" disabled={busy||items.some(active)||recovery} onclick={()=>restore(item)}>确认恢复此备份</button>{/if}
 {#if item.kind==="restore"&&["failed","validated","restored"].includes(item.status)}<button disabled={busy||items.some(active)||recovery} onclick={()=>validate(item)}>重新验证</button>{/if}
 <button disabled={busy||items.some(active)||recovery} onclick={()=>remove(item)}>删除这份文件</button></div></article>{/each}
 {#if !items.length}<p class="muted">还没有备份。创建后建议下载一份保存到另一台设备。</p>{/if}
</section>
<style>.backup-item{padding:1.2rem 0;border-bottom:1px solid var(--line-divider)}.backup-item small{overflow-wrap:anywhere}progress{width:100%;accent-color:var(--primary)}</style>
