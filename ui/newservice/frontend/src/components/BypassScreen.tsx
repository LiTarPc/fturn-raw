import {useState} from 'react';
import {IconApps,IconPlus,IconTrash,IconRefresh,IconWorld} from '@tabler/icons-react';
import {api,native} from '../api';
import type {Snapshot,BypassSettings} from '../types';
import {ScreenHeader} from './ScreenHeader';
import {Toggle} from './Toggle';
type Invoke=(fn:()=>Promise<unknown>)=>Promise<boolean>;
function RuleList({title,hint,placeholder,values,disabled,icon,onChange}:{title:string;hint:string;placeholder:string;values:string[];disabled:boolean;icon:React.ReactNode;onChange:(v:string[])=>Promise<boolean>}){
 const [text,setText]=useState('');
 const add=async()=>{const v=text.trim();if(v&&await onChange([...values,v]))setText('')};
 return <section className="bypass-rules"><h2>{icon}{title}</h2><p className="hint">{hint}</p>
  <form className="bypass-add" onSubmit={e=>{e.preventDefault();void add()}}><input aria-label={title} value={text} onChange={e=>setText(e.target.value)} placeholder={placeholder} disabled={disabled} maxLength={1024}/><button className="action secondary" type="submit" disabled={disabled||!text.trim()||values.length>=512} aria-label={`Добавить: ${title}`}><IconPlus size={18}/></button></form>
  <ul className="bypass-list">{values.map(v=><li key={v}><span title={v}>{v}</span><button className="icon-button" type="button" disabled={disabled} aria-label={`Удалить ${v}`} onClick={()=>void onChange(values.filter(x=>x!==v))}><IconTrash size={15}/></button></li>)}{values.length===0&&<li className="hint">Пока нет правил</li>}</ul>
 </section>
}
export function BypassScreen({snapshot:s,busy,running,invoke,onBack}:{snapshot?:Snapshot;busy:boolean;running:boolean;invoke:Invoke;onBack:()=>void}){
 const b=s?.bypass,v=b?.settings,disabled=busy||!v;
 const save=(next:BypassSettings)=>invoke(()=>api.SaveBypassSettings(next));
 const toggle=(key:'enabled'|'ru')=>{if(v)void save({...v,[key]:!v[key]})};
 return <section className="screen bypass-screen"><ScreenHeader title="Обход" onBack={onBack}/>
  <p className="hint">Общие правила для всех профилей. Выбранные TCP/UDP-соединения идут через внешнее подключение Raw: текущий системный VPN или обычный интернет.</p>
  <Toggle label="Включить обход" checked={v?.enabled??false} disabled={disabled} onChange={()=>toggle('enabled')}/>
  <p className="hint bypass-state">{running?'Изменения сохраняются сейчас и применятся при следующем подключении.':'Правила применятся при подключении Raw.'}</p>
  {b?.error&&<p className="notice error">{b.error}</p>}
  <Toggle label="Обход RU-сетей" checked={v?.ru??false} disabled={disabled} onChange={()=>toggle('ru')}/>
  <div className="core-card"><strong><IconRefresh size={15}/> RU CIDR</strong><p className="hint">{b?.ruCount.toLocaleString('ru-RU')??'…'} IPv4-сетей · {b?.ruUpdated??'…'}<br/>Источник: IPdeny. При ошибке обновления сохраняется прежний список.</p><button className="action secondary" disabled={disabled||running||!native} onClick={()=>void invoke(()=>api.UpdateRUCIDR())}>Обновить RU CIDR</button>{running&&<p className="hint">Обновление доступно после отключения Raw.</p>}</div>
  <RuleList title="Сайты и адреса напрямую" icon={<IconWorld size={15}/>} hint="Домен и его поддомены, IPv4 или CIDR. Например: example.org, .ru, 203.0.113.0/24. Для международных доменов используйте punycode." placeholder="example.org" values={v?.sites??[]} disabled={disabled} onChange={sites=>v?save({...v,sites}):Promise.resolve(false)}/>
  <RuleList title="Приложения напрямую" icon={<IconApps size={15}/>} hint="Имя .exe или полный путь. Дочерние процессы с другим именем добавляются отдельно." placeholder="steam.exe" values={v?.apps??[]} disabled={disabled} onChange={apps=>v?save({...v,apps}):Promise.resolve(false)}/>
  <p className="hint">DNS на портах 53/853 остаётся в Raw; IPv6 блокируется. Домены определяются по IPv4: общий адрес CDN может затронуть другие сайты. DoH браузера скрывает поддомены; для точного выбора добавьте IPv4/CIDR или приложение. ICMP и фрагментированные исходящие пакеты идут через Raw.</p>
 </section>
}
