import {IconX} from '@tabler/icons-react';
export function ScreenHeader({title,onBack}:{title:string;onBack:()=>void}){
 return <header className="screen-header"><h1>{title}</h1><button type="button" className="icon-button" aria-label="Закрыть раздел" title="Назад" onClick={onBack}><IconX size={19}/></button></header>
}
