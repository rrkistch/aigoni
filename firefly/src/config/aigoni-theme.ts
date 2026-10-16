// The adapter changes configuration values, not upstream components or layout.
const raw=import.meta.env?.PUBLIC_AIGONI_THEME || (typeof process!=="undefined"?process.env.PUBLIC_AIGONI_THEME:undefined);
const overrides:Record<string,unknown>=raw?JSON.parse(raw):{};
export const themeDefaults:Record<string,unknown>={};
function merge(base:any,patch:any):any{
 if(Array.isArray(patch))return patch.map((item)=>{
  if(item&&typeof item==="object"&&!Array.isArray(item)){
   const original=Array.isArray(base)?(item.type?base.find(v=>v.type===item.type):undefined):undefined;
   return merge(original||{},item);
  }
  return item;
 });
 if(patch&&typeof patch==="object"){
  const out={...(base||{})};for(const [key,value] of Object.entries(patch))out[key]=merge(out[key],value);return out;
 }
 return patch;
}
export function applyThemeGroup<T extends object>(name:string,target:T):void{
 themeDefaults[name]=JSON.parse(JSON.stringify(target));
 if(!(name in overrides))return;
 const next=merge(target,overrides[name]);
 if(Array.isArray(target))target.splice(0,target.length,...next);
 else Object.assign(target,next);
}

export function themeGroup(name:string):any{return overrides[name];}
