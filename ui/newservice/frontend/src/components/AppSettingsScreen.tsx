import {IconSettings2} from '@tabler/icons-react';
import {AppVersionCard} from './AppVersionCard';
import {native} from '../api';
import type {Snapshot,AppSettings} from '../api';
import {ScreenHeader} from './ScreenHeader';
import {Toggle} from './Toggle';
export function AppSettingsScreen({snapshot:s,busy,running,invoke,onChange,onBack,onExit}:{snapshot?:Snapshot;busy:boolean;running:boolean;invoke:(fn:()=>Promise<unknown>)=>Promise<boolean>;onChange:(s:AppSettings)=>void;onBack:()=>void;onExit:()=>void}){
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
  {native&&settings?.minimizeToTray&&!s?.trayReady&&<p className="notice">Трей пока недоступен. Окно останется на панели задач.</p>}
  <AppVersionCard busy={busy} running={running} invoke={invoke}/>
  <button className="action secondary exit-action" disabled={busy||!native} onClick={onExit}>Выход из приложения</button>
 </section>
}
