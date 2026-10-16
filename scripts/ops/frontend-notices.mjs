// Preserve actual installed frontend dependency licenses in the distributed app.
import {readdir,readFile,writeFile} from 'node:fs/promises'
import path from 'node:path'
const output=[]
async function visit(dir){for(const ent of await readdir(dir,{withFileTypes:true})){if(!ent.isDirectory())continue;const root=path.join(dir,ent.name);if(ent.name.startsWith('@')){await visit(root);continue}try{const p=JSON.parse(await readFile(path.join(root,'package.json'),'utf8'));const texts=[];for(const name of await readdir(root)){if(/^(license|copying|notice)(\.|$)/i.test(name))texts.push(await readFile(path.join(root,name),'utf8'))}output.push(`${p.name} ${p.version} (${p.license??'license metadata missing'})\n${texts.join('\n')}`)}catch{ /* optional packages without a license file retain metadata */ }}}
await visit('node_modules');await writeFile('dist/THIRD_PARTY_NOTICES.txt',output.sort().join('\n\n'))
