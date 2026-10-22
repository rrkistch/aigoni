<script lang="ts">
 import {onMount,tick} from "svelte";
 import {api,uploadMedia} from "./api";
 let items=$state<any[]>([]);let busy=$state(false);let progress=$state(0);let error=$state("");let message=$state("");let kind=$state("");let replacing=$state<any>(null);let replacement=$state("");
 async function load(){items=(await api("/media")).items;}
 onMount(()=>{void load().catch(e=>error=e.message);});
 async function upload(e:Event){const input=e.currentTarget as HTMLInputElement;const file=input.files?.[0];if(!file)return;input.value="";busy=true;error="";message="";progress=0;try{const uploaded=await uploadMedia(file,p=>progress=p);await load();if(replacing&&uploaded.kind===replacing.kind)replacement=uploaded.id;message="素材已上传，可以在文章或外观设置中使用。";}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function remove(item:any){if(!confirm(`删除「${item.name}」？`))return;try{await api("/media/"+item.id,"DELETE");await load();}catch(e){error=(e as Error).message;}}
 async function beginReplace(item:any){replacing=item;replacement="";await tick();document.getElementById("media-replacement")?.scrollIntoView({block:"center"});}
 async function rename(item:any){const name=prompt("素材显示名称（不改变版本地址）",item.name);if(!name||name===item.name)return;try{await api("/media/"+item.id,"PATCH",{name,revision:item.revision||0});await load();message="名称已更新，已有引用不变。";}catch(e){error=(e as Error).message;}}
 async function replace(){const next=items.find(v=>v.id===replacement);if(!replacing||!next||!confirm(`把「${replacing.name}」当前所有可编辑引用改为「${next.name}」？历史、待审批候选和旧公开版本保留原文件。`))return;busy=true;error="";try{const result=await api(`/media/${replacing.id}/replace`,"POST",{replacement_id:next.id,revision:replacing.revision||0,replacement_revision:next.revision||0,confirm:true});replacing=null;replacement="";await load();message=result.message;}catch(e){error=(e as Error).message;}finally{busy=false;}}
 async function copy(path:string){try{await navigator.clipboard.writeText(path);message="地址已复制。";}catch{message="请选中下方素材地址复制："+path;}}
</script>
<section class="card-base panel stack"><div class="row spread"><div><h1>公开素材库</h1><p class="muted">这里的文件可被任何访客访问。私人资料请上传到私人笔记附件。</p></div><label class="upload">上传素材<input type="file" disabled={busy} accept=".png,.jpg,.jpeg,.gif,.webp,.avif,.mp3,.wav,.ogg,.m4a,.flac,.lrc" onchange={upload} /></label></div>
 <p class="muted">图片最大 20 MiB，音频 100 MiB，LRC 歌词 1 MiB。上传中断后重新选择同一文件即可继续。</p>
 {#if busy}<progress max="100" value={progress} aria-label="上传进度"></progress><p role="status">已上传 {progress}%</p>{/if}
 {#if error}<div class="notice error" role="alert">{error}</div>{/if}{#if message}<div class="notice" role="status">{message}</div>{/if}
 {#if replacing}<article id="media-replacement" class="stack replacement"><h2>替换「{replacing.name}」</h2><p class="muted">先选择同类型新素材，或使用上方上传。执行时更新当前文章、页面、笔记、Wiki和设置引用；不修改历史、待审批候选或旧公开页面，旧文件保留到相关引用释放。</p><label>替代素材<select aria-label="替代素材" bind:value={replacement}><option value="">请选择已上传素材</option>{#each items.filter(v=>v.kind===replacing.kind&&v.id!==replacing.id) as option}<option value={option.id}>{option.name}</option>{/each}</select></label><div class="row"><button class="primary" disabled={busy||!replacement} onclick={replace}>确认替换当前引用</button><button disabled={busy} onclick={()=>replacing=null}>取消</button></div></article>{/if}
 <label>类型<select aria-label="素材类型" bind:value={kind}><option value="">全部</option><option value="image">图片</option><option value="audio">音频</option><option value="lyrics">歌词</option></select></label>
 <div class="media-grid">{#each items.filter(item=>!kind||item.kind===kind) as item}<article class="stack media-item">
 {#if item.kind==="image"}<img src={item.path} alt={item.name} loading="lazy" onload={(e)=>{item.width=e.currentTarget.naturalWidth;item.height=e.currentTarget.naturalHeight;}} />{:else if item.kind==="audio"}<audio controls preload="none" src={item.path}></audio>{:else}<span class="pill">LRC 歌词</span>{/if}
 <strong>{item.name}</strong>{#if item.replaced_by}<small class="muted">已用另一版本替换当前引用；此版本仍按原地址保留。</small>{/if}<small class="muted">{(item.size/1024).toFixed(1)} KiB {item.width?` · ${item.width} × ${item.height}`:""}</small><input aria-label="素材地址" readonly value={item.path} />
 {#if item.references?.length}<details><summary>被 {item.references.length} 处引用</summary>{#each item.references as ref}<p>{ref}</p>{/each}</details>{/if}
 <div class="row"><button onclick={()=>copy(item.path)}>复制地址</button><button disabled={busy} onclick={()=>rename(item)}>重命名</button><button disabled={busy} onclick={()=>beginReplace(item)}>替换使用处</button><button disabled={busy||item.references?.length>0} onclick={()=>remove(item)}>删除</button></div></article>{/each}</div>
 {#if !items.length}<p class="muted">还没有公开素材。先上传壁纸、封面或歌曲吧。</p>{/if}
</section>
<style>.media-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(min(100%,260px),1fr));gap:1.5rem}.media-item{min-width:0}.media-item img{width:100%;height:160px;object-fit:cover;border-radius:var(--radius-large)}audio{width:100%}progress{width:100%;accent-color:var(--primary)}.replacement{padding:1rem;border:1px solid var(--line-divider);border-radius:1rem}</style>
