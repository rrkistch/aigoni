<script lang="ts">
 import ConfigFields from "./ConfigFields.svelte";
 import {api,uploadMedia} from "./api";
 let picker=$state(false);let assets=$state<any[]>([]);let mediaError=$state("");let uploading=$state(false);
 async function choose(){picker=!picker;mediaError="";if(picker){try{assets=(await api("/media")).items.filter((item:any)=>item.kind===schema.mediaKind);}catch(e){mediaError=(e as Error).message;}}}
 async function upload(e:Event){const file=(e.currentTarget as HTMLInputElement).files?.[0];if(!file)return;uploading=true;mediaError="";try{const item=await uploadMedia(file,()=>{});onchange(item.path);picker=false;}catch(e){mediaError=(e as Error).message;}finally{uploading=false;}}
 let {schema,value,onchange,path="config"}: {schema:any;value:any;onchange:(v:any)=>void;path?:string}=$props();
 function empty(s:any):any{return s.type==="object"?{}:s.type==="array"?[]:s.type==="boolean"?false:s.type==="number"?(s.min??0):(s.enum?.[0]??"");}
 function set(key:string,v:any){onchange({...value,[key]:v});}
 function item(index:number,v:any){const next=[...(value||[])];next[index]=v;onchange(next);}
 function move(index:number,delta:number){const next=[...(value||[])];[next[index],next[index+delta]]=[next[index+delta],next[index]];onchange(next);}
</script>
{#if schema.type==="object"}
 <div class="stack">{#if path!=="config"}<h3>{schema.label}</h3>{/if}{#each Object.entries(schema.properties||{}) as [key,field]}
 {#if !(field as any).visibleWhen || Object.entries((field as any).visibleWhen).every(([k,v])=>value?.[k]===v)}
  <ConfigFields schema={field} value={value?.[key]} onchange={(v)=>set(key,v)} path={path+"-"+key} />
 {/if}{/each}</div>
{:else if schema.type==="array"}
 <fieldset class="stack config-array"><legend>{schema.label}</legend>
 {#each (value||[]) as entry,index}
 <div class="stack config-entry"><div class="row spread"><strong>{schema.items.type==="object"?(entry.name||entry.title||entry.type||schema.items.label)+" "+(index+1):(index+1)}</strong><div class="row"><button type="button" aria-label="上移" disabled={index===0} onclick={()=>move(index,-1)}>↑</button><button type="button" aria-label="下移" disabled={index===value.length-1} onclick={()=>move(index,1)}>↓</button><button type="button" onclick={()=>onchange(value.filter((_:any,i:number)=>i!==index))}>移除</button></div></div>
 <ConfigFields schema={schema.items} value={entry} onchange={(v)=>item(index,v)} path={path+"-"+index} />
 </div>{/each}
 <button type="button" disabled={(value?.length||0)>=schema.maxItems} onclick={()=>onchange([...(value||[]),empty(schema.items)])}>＋ 添加{schema.items.label}</button>
 </fieldset>
{:else if schema.type==="boolean"}
 <label class="row"><input type="checkbox" checked={value??false} onchange={(e)=>onchange(e.currentTarget.checked)} />{schema.label}</label>
{:else if schema.type==="number"}
 <label>{schema.label}<input type="number" min={schema.min} max={schema.max} step={schema.max<=1?0.05:1} value={value??""} oninput={(e)=>{if(e.currentTarget.value!=="")onchange(Number(e.currentTarget.value));}} /></label>
{:else if schema.enum}
 <label>{schema.label}<select aria-label={schema.label} value={value??""} onchange={(e)=>onchange(e.currentTarget.value)}>{#if value===undefined}<option value="">使用主题默认</option>{/if}{#each schema.enum as option}<option value={option}>{({light:"浅色",dark:"深色",system:"跟随系统",banner:"横幅",fullscreen:"全屏",overlay:"透明覆盖",none:"无",local:"本地列表",meting:"外部歌单",left:"左侧",right:"右侧",both:"双侧",list:"列表循环",one:"单曲循环",random:"随机播放",top:"顶部",sticky:"跟随滚动"} as Record<string,string>)[option]||option||"继承默认"}</option>{/each}</select></label>
{:else if schema.multiline}
 <label>{schema.label}<textarea rows="4" value={value??""} oninput={(e)=>onchange(e.currentTarget.value)}></textarea></label>
{:else}
 <label>{schema.label}<input type="text" value={value??""} oninput={(e)=>onchange(e.currentTarget.value)} />{#if schema.format==="url"}<small>站内资源路径或完整 HTTP(S) 地址</small>{/if}</label>
 {#if schema.mediaKind}<div class="row"><button type="button" onclick={choose}>从素材库选择</button><label class="upload">{uploading?"上传中…":"上传文件"}<input type="file" disabled={uploading} accept={schema.mediaKind==="image"?".png,.jpg,.jpeg,.gif,.webp,.avif":schema.mediaKind==="audio"?".mp3,.wav,.ogg,.m4a,.flac":".lrc"} onchange={upload} /></label></div>
 {#if mediaError}<p role="alert">{mediaError}</p>{/if}
 {#if picker}<div class="media-options">{#each assets as asset}<button type="button" onclick={()=>{onchange(asset.path);picker=false;}}>{#if schema.mediaKind==="image"}<img src={asset.path} alt="" loading="lazy" />{/if}{asset.name}</button>{/each}{#if !assets.length}<p>暂无此类素材，可直接上传。</p>{/if}</div>{/if}
 {/if}
{/if}
<style>
 .config-array{border:0;padding:0;margin:0;min-width:0}.config-array legend{font-weight:650;margin-bottom:.6rem}.config-entry{border-inline-start:3px solid var(--primary);padding:1rem;background:var(--btn-regular-bg);border-radius:var(--radius-large)}
 .media-options{display:flex;flex-wrap:wrap;gap:.75rem}.media-options button{display:flex;flex-direction:column;gap:.5rem;max-width:180px}.media-options img{width:120px;height:70px;object-fit:cover;border-radius:.5rem}
</style>
