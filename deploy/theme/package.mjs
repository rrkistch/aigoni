import fs from 'node:fs/promises';
import path from 'node:path';
import {execFileSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {createReadStream} from 'node:fs';
const {THEME_VERSION:version,SOURCE_COMMIT:commit}=process.env;
if(process.platform!=='linux'||process.arch!=='x64'||!/^theme-v\d+\.\d+\.\d+$/.test(version||'')||!/^[a-f0-9]{40}$/.test(commit||'')||Number(process.versions.node.split('.')[0])!==24)throw new Error('Invalid theme release environment');
const root='/bundle',source=path.join(root,'source');
// Installed dependencies are authoritative. pnpm 11's timestamp/path checks must not reinstall them.
await fs.appendFile(path.join(source,'pnpm-workspace.yaml'),'\n# Release bundle: dependencies are preinstalled and verified after relocation.\nverifyDepsBeforeRun: false\n');
const relocated=path.join(root,'relocated-source');
await fs.rename(source,relocated);
execFileSync(process.execPath,[path.join(relocated,'scripts/aigoni-build.mjs'),path.join(relocated,'tests/fixtures/public-snapshot.json'),path.join(root,'relocation-check')],{cwd:relocated,stdio:'inherit',env:{...process.env,ASTRO_TELEMETRY_DISABLED:'1'}});
await fs.rename(relocated,source);

// Font files are release assets, not disposable dependency-optimizer caches.
const fontCache=path.join(source,'node_modules/.astro/fonts'),savedFonts=path.join(root,'original-fonts');
await fs.rename(fontCache,savedFonts);
for(const name of ['dist','.astro','.aigoni','.vercel','.wrangler','node_modules/.astro','node_modules/.cache'])await fs.rm(path.join(source,name),{recursive:true,force:true});
await fs.mkdir(path.dirname(fontCache),{recursive:true});await fs.rename(savedFonts,fontCache);
await fs.copyFile(path.join(root,'firefly-upstream.json'),path.join(source,'aigoni-upstream.json'));
const manifest={format:1,dependencies:'bundled-portable-v1',assets:'original-fonts-v1',version,source_commit:commit,os:'linux',arch:'amd64',node_major:24,pnpm:'11.22.0',admin_api:1,settings:1};
await fs.writeFile(path.join(root,'manifest.json'),JSON.stringify(manifest,null,2));
await fs.mkdir('/out',{recursive:true});
const name=`aigoni-${version}-linux-amd64.tar.gz`,file=path.join('/out',name);
// pnpm needs relative symlinks; hard links are materialized, never extracted as archive hard links.
execFileSync('tar',['--hard-dereference','-czf',file,'-C',root,'manifest.json','source'],{stdio:'inherit'});
const info=await fs.stat(file);if(info.size>1024**3)throw new Error('Theme package exceeds 1 GiB');
const hash=createHash('sha256');for await(const chunk of createReadStream(file))hash.update(chunk);
await fs.writeFile('/out/SHA256SUMS',`${hash.digest('hex')}  ${name}\n`);
console.log(`Ready: ${name} (${Math.round(info.size/1024/1024)} MiB)`);
