import {IconSettings2,IconCpu} from '@tabler/icons-react';
import {native} from '../api';
import type {Snapshot,AppSettings} from '../api';
import {ScreenHeader} from './ScreenHeader';
function Toggle({label,checked,disabled,onChange}:{label:string;checked:boolean;disabled:boolean;onChange:()=>void}){
 return <div className="setting-row"><span>{label}</span><button type="button" className={`toggle${checked?' on':''}`} role="switch" aria-label={label} aria-checked={checked} disabled={disabled} onClick={onChange}><span/></button></div>
}
export function AppSettingsScreen({snapshot:s,busy,onChange,onBack,onExit}:{snapshot?:Snapshot;busy:boolean;onChange:(s:AppSettings)=>void;onBack:()=>void;onExit:()=>void}){
 const settings=s?.settings;
 const toggle=(key:keyof AppSettings)=>{if(settings)onChange({...settings,[key]:!settings[key]})};
 return <section className="screen app-settings">
  <ScreenHeader title="Настройки" onBack={onBack}/>
  <div className="setting-context"><span><IconSettings2 size={15}/> Параметры соединения</span><small>В редакторе профиля</small></div>
  <div className="settings-group">
   <Toggle label="Сворачивать в трей" checked={settings?.minimizeToTray??true} disabled={busy||!s} onChange={()=>toggle('minimizeToTray')}/>
   <Toggle label="Запускать с Windows" checked={settings?.startWithWindows??false} disabled={busy||!s} onChange={()=>toggle('startWithWindows')}/>
   <Toggle label="Авто-подключение" checked={settings?.autoConnect??false} disabled={busy||!s} onChange={()=>toggle('autoConnect')}/>
  </div>
  <p className="hint">{settings?.minimizeToTray?'Закрытие и сворачивание окна оставляют Raw в трее. Для завершения выбери «Выход».':'Закрытие окна отключает Raw и завершает приложение.'}<br/>Авто-подключение использует выбранный профиль при запуске приложения.</p>
  {native&&settings?.minimizeToTray&&!s?.trayReady&&<p className="notice">Трей пока недоступен. Окно останется на панели задач.</p>}
  <div className="core-card"><strong><IconCpu size={17}/> Ядро Raw</strong><p className="hint">raw-client.exe · TCP к VK TURN<br/>MTU и потоки настраиваются отдельно для каждого сервера.</p><p className="hint">Для экспериментального Raw пока нет канала автоматических обновлений.</p></div>
  <p className="hint data-path">Профили и настройки<br/>{s?.dataDir??"%LOCALAPPDATA%\\fturn-raw"}</p>
  <button className="action secondary exit-action" disabled={busy||!native} onClick={onExit}>Выход из приложения</button>
 </section>
}
