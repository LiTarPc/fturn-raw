import type {Backend,SavedProfile,Snapshot} from './types';
const profiles: SavedProfile[] = ['192.0.2.1','198.51.100.2'].map((host,i)=>({id:String(i),name:i?'Второй сервер':'Основной сервер',profile:{Server:host+':56010',VkLink:'https://vk.ru/call/join/demo',Key:'ab'.repeat(32),Mtu:1420,Streams:10,StreamsPerCred:5,RouteMode:'full'}}));
const demo: Snapshot = { authRetryAt:0, bypassActive:false, bypass:{settings:{enabled:false,ru:false,sites:[],apps:[]},ruCount:8652,ruUpdated:"2026-10-03 · встроенный",error:""}, dataDir:"%LOCALAPPDATA%\\fturn-raw", elapsedSeconds:0,settings:{minimizeToTray:true,startWithWindows:false,autoConnect:false},trayReady:false, profile:profiles[0].profile,profiles,activeId:'0', state:'idle', detail:'Готов к подключению', underlay:'Автоматически: текущий VPN / Ethernet / Wi-Fi', ready:0, logs:[],rx:0,tx:0 };
const unavailable = async (): Promise<never> => { throw new Error('Действие доступно в приложении Windows.'); };
export const previewApi: Backend = {
 SaveBypassSettings:async(s)=>{demo.bypass.settings={...s,sites:[...s.sites],apps:[...s.apps]}}, UpdateRUCIDR:unavailable,
 SaveAppSettings:async(s)=>{demo.settings=s}, Exit:unavailable,
 GetSnapshot:async()=>({...demo,profiles:[...demo.profiles]}),
 SaveProfile:async(p)=>{demo.profile=p},
 SaveNamedProfile:async(p,name,create)=>{const running=['connecting','connected','reconnecting','stopping'].includes(demo.state);if(create){const id=String(Date.now());demo.profiles.push({id,name,profile:p});if(!running){demo.activeId=id;demo.profile=p}}else{if(running)throw new Error('Отключи Raw для изменения активного профиля.');const entry=demo.profiles.find(x=>x.id===demo.activeId)!;entry.name=name;entry.profile=p;demo.profile=p}},
 SelectProfile:async(id)=>{const p=demo.profiles.find(x=>x.id===id);if(p){demo.activeId=id;demo.profile=p.profile}},
 DeleteProfile:async(id)=>{if(demo.profiles.length<=1)throw new Error('Последний профиль нельзя удалить.');demo.profiles=demo.profiles.filter(x=>x.id!==id);if(demo.activeId===id){demo.activeId=demo.profiles[0].id;demo.profile=demo.profiles[0].profile}},
 Connect:unavailable, Disconnect:async()=>{}, ExportProfile:unavailable, ImportProfile:unavailable, CopyConnectionLink:unavailable
};
