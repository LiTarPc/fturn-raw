import {useState} from 'react';
import {IconPower,IconTerminal2,IconMoon,IconSun,IconSettings2} from '@tabler/icons-react';
import {themeStore} from '../lib/stores/themeStore';
export type Page='vpn'|'logs'|'editor'|'settings';
export function Sidebar({page,onNavigate}:{page:Page;onNavigate:(p:Page)=>void}){
 const [theme,setTheme]=useState(themeStore.get());
 return <aside className="sidebar"><div className="sidebar-top">
  {[{id:'vpn' as Page,icon:<IconPower size={22}/>,text:'vpn'},{id:'logs' as Page,icon:<IconTerminal2 size={22}/>,text:'логи'}].map(n=><button key={n.id} className={`nav-btn${page===n.id?' nav-btn--active':''}`} onClick={()=>onNavigate(n.id)} title={n.text}>{n.icon}<span className="nav-label">{n.text}</span></button>)}
 </div><div className="sidebar-bottom">
  <button className="theme-toggle" title="Сменить тему" onClick={()=>{themeStore.toggle();setTheme(themeStore.get())}}>{theme==='light'?<IconMoon size={18}/>:<IconSun size={18}/>}</button>
  <button className={`nav-btn${page==='settings'?' nav-btn--active':''}`} onClick={()=>onNavigate('settings')} title="Настройки приложения"><IconSettings2 size={22}/><span className="nav-label">настр.</span></button>
 </div></aside>
}
